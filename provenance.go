package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

const processRunCrate = "https://w3id.org/ro/wfrun/process/0.5"

//go:embed profiles/bronze/profile.json
var bronzeProfileJSON []byte

type instrument struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
}

// The --provenance file: how a dataset was produced.
type provenance struct {
	DerivedFrom string            `json:"derived_from,omitempty"`
	Instruments []instrument      `json:"instruments,omitempty"`
	Properties  map[string]string `json:"properties"`
}

// Without a path, the dataset has no parent and no properties.
func readProvenance(path string) (provenance, error) {
	var p provenance
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return provenance{}, err
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&p); err == nil && dec.More() {
			err = errors.New("unexpected content after the JSON object")
		}
		if err == nil {
			err = p.check()
		}
		if err != nil {
			return provenance{}, fmt.Errorf("%s is not a valid provenance file: %w", path, err)
		}
	}
	if p.Properties == nil {
		p.Properties = map[string]string{}
	}
	return p, nil
}

func (p provenance) check() error {
	if (p.DerivedFrom == "") != (len(p.Instruments) == 0) {
		return errors.New("derived_from and instruments are given together or not at all")
	}
	if p.DerivedFrom != "" && !validID.MatchString(p.DerivedFrom) {
		return fmt.Errorf("derived_from %q is not a dataset ID", p.DerivedFrom)
	}
	for i, in := range p.Instruments {
		if in.Name == "" || in.Version == "" || in.URL == "" {
			return fmt.Errorf("instruments[%d] needs a name, version and url", i)
		}
		if u, err := url.Parse(in.URL); err != nil || !u.IsAbs() {
			return fmt.Errorf("instruments[%d] url %q is not an absolute URL", i, in.URL)
		}
	}
	for _, k := range []string{"subscription_name", "subscription_id", "source_path"} {
		if _, ok := p.Properties[k]; ok {
			return fmt.Errorf("property %q is set by lbf and cannot be supplied", k)
		}
	}
	return nil
}

type rule struct {
	id     string
	schema *jsonschema.Schema
}

// The chosen profile and every profile it builds on through $ref, bronze first; ID is the chosen one.
type profile struct {
	ID    string
	rules []rule
}

// Loads <dir>/profile.json, or just lbf's bronze profile when dir is empty. Sibling profiles and the
// bronze profile are registered by $id, so `$ref` between them resolves without the network.
func loadProfile(dir string) (*profile, error) {
	bronzeID, _, err := schemaHead(bronzeProfileJSON)
	if err != nil {
		return nil, err
	}
	docs := map[string][]byte{bronzeID: bronzeProfileJSON}
	parents := map[string]string{}
	targetID := bronzeID

	if dir != "" {
		target := filepath.Join(dir, "profile.json")
		if info, err := os.Stat(dir); err == nil && !info.IsDir() {
			target = dir
		}
		siblings, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(target)), "*", "profile.json"))
		for _, path := range append(siblings, target) {
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			id, parent, err := schemaHead(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			docs[id], parents[id] = raw, parent
			if path == target {
				targetID = id
			}
		}
	}

	var chain []string
	for id := targetID; id != "" && id != bronzeID && !slices.Contains(chain, id); id = parents[id] {
		chain = append([]string{id}, chain...)
	}
	ids := append([]string{bronzeID}, chain...)

	c := jsonschema.NewCompiler()
	c.AssertFormat()
	for id, raw := range docs {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		if err := c.AddResource(id, doc); err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
	}
	p := &profile{ID: targetID}
	for _, id := range ids {
		sch, err := c.Compile(id)
		if err != nil {
			return nil, fmt.Errorf("profile %s: %w", id, err)
		}
		p.rules = append(p.rules, rule{id, sch})
	}
	return p, nil
}

func (p *profile) ids() []string {
	ids := make([]string, len(p.rules))
	for i, r := range p.rules {
		ids[i] = r.id
	}
	return ids
}

func (p *profile) validate(view map[string]any) error {
	raw, err := json.Marshal(view)
	if err != nil {
		return err
	}
	for _, r := range p.rules {
		if err := validateJSON(r.schema, raw); err != nil {
			return fmt.Errorf("dataset does not meet profile %s:\n%w", r.id, err)
		}
	}
	return nil
}

func validateJSON(sch *jsonschema.Schema, raw []byte) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	err = sch.Validate(inst)
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return err
	}
	var problems []string
	collectLeaves(verr, &problems)
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "\n"))
}

func collectLeaves(e *jsonschema.ValidationError, out *[]string) {
	if len(e.Causes) == 0 {
		loc := "/" + strings.Join(e.InstanceLocation, "/")
		*out = append(*out, fmt.Sprintf("  %s: %s", loc, e.ErrorKind.LocalizedString(message.NewPrinter(language.English))))
		return
	}
	for _, c := range e.Causes {
		collectLeaves(c, out)
	}
}

// A profile's $id, recorded as the crate's conformsTo, and the profile it builds on, if any.
func schemaHead(raw []byte) (id, parent string, err error) {
	var head struct {
		ID  string `json:"$id"`
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return "", "", err
	}
	if head.ID == "" {
		return "", "", errors.New("no $id; it is recorded as the crate's conformsTo")
	}
	return head.ID, head.Ref, nil
}
