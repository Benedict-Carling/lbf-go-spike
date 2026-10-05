package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

const processRunCrate = "https://w3id.org/ro/wfrun/process/0.5"

var lbfProperties = []string{"subscription_name", "subscription_id", "source_path"}

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

// Where publish takes its provenance from: a --provenance file, or the flags that spell it out.
type provenanceFlags struct {
	file, derivedFrom, properties string
	instruments                   []instrument
}

func (f provenanceFlags) load() (provenance, error) {
	if f.file != "" {
		if f.derivedFrom != "" || len(f.instruments) > 0 || f.properties != "" {
			return provenance{}, errors.New("--provenance cannot be combined with --derived-from, --instrument or --properties")
		}
		return readProvenance(f.file)
	}
	p := provenance{DerivedFrom: f.derivedFrom, Instruments: f.instruments, Properties: map[string]string{}}
	if f.properties != "" {
		err := decodeStrict(f.properties, &p.Properties)
		if err == nil && p.Properties == nil {
			err = errors.New("want a JSON object of property names and values")
		}
		if err != nil {
			return provenance{}, fmt.Errorf("%s is not a valid properties file: %w", f.properties, err)
		}
	}
	if err := p.check(); err != nil {
		return provenance{}, err
	}
	return p, nil
}

func (p provenance) sameAs(q provenance) bool {
	return p.DerivedFrom == q.DerivedFrom && slices.Equal(p.Instruments, q.Instruments) && maps.Equal(p.Properties, q.Properties)
}

func readProvenance(path string) (provenance, error) {
	var p provenance
	if path != "" {
		err := decodeStrict(path, &p)
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

func decodeStrict(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(v); err == nil && dec.More() {
		err = errors.New("unexpected content after the JSON object")
	}
	return err
}

// --instrument name=NAME,version=VERSION,url=URL; a comma not followed by a known key stays in the value.
func parseInstrument(s string) (instrument, error) {
	var in instrument
	fields := map[string]*string{"name": &in.Name, "version": &in.Version, "url": &in.URL}
	var last *string
	for part := range strings.SplitSeq(s, ",") {
		if k, v, ok := strings.Cut(part, "="); ok && fields[k] != nil {
			*fields[k], last = v, fields[k]
			continue
		}
		if last == nil {
			return instrument{}, fmt.Errorf("%q is not name=NAME,version=VERSION,url=URL", s)
		}
		*last += "," + part
	}
	return in, nil
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
	for _, k := range lbfProperties {
		if _, ok := p.Properties[k]; ok {
			return fmt.Errorf("property %q is set by lbf and cannot be supplied", k)
		}
	}
	for k := range p.Properties {
		if k == "uploader" || k == "blob-location" || k == "run" || strings.HasPrefix(k, "source-") {
			return fmt.Errorf("property %q would clash with an entity in the crate; choose another name", k)
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

// Profiles are registered by $id so `$ref` between siblings and bronze resolves without the network.
func loadProfile(dir string) (*profile, error) {
	bronzeID, _, err := schemaHead(bronzeProfileJSON)
	if err != nil {
		return nil, err
	}
	docs := map[string][]byte{bronzeID: bronzeProfileJSON}
	parents := map[string]string{}
	targetID := bronzeID

	if dir != "" {
		if dir, err = filepath.Abs(dir); err != nil {
			return nil, err
		}
		target := filepath.Join(dir, "profile.json")
		if info, err := os.Stat(dir); err == nil && !info.IsDir() {
			target = dir
		}
		// Siblings are profiles/<name>/profile.json or, versioned, profiles/<name>/<version>/profile.json.
		parent := filepath.Dir(filepath.Dir(target))
		siblings, _ := filepath.Glob(filepath.Join(parent, "*", "profile.json"))
		versioned, _ := filepath.Glob(filepath.Join(filepath.Dir(parent), "*", "*", "profile.json"))
		for _, path := range slices.Concat(siblings, versioned, []string{target}) {
			var id, ref string
			raw, err := os.ReadFile(path)
			if err == nil {
				id, ref, err = schemaHead(raw)
			}
			if err != nil && path != target {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if id == bronzeID {
				return nil, fmt.Errorf("%s: $id %s is lbf's bronze profile, which only lbf defines", path, id)
			}
			docs[id], parents[id] = raw, ref
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
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	for _, r := range p.rules {
		if err := validateJSON(r.schema, inst); err != nil {
			return fmt.Errorf("dataset does not meet profile %s:\n%w", r.id, err)
		}
	}
	return nil
}

func validateJSON(sch *jsonschema.Schema, inst any) error {
	err := sch.Validate(inst)
	verr, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return err
	}
	problems := collectLeaves(verr, message.NewPrinter(language.English))
	slices.Sort(problems)
	return errors.New(strings.Join(problems, "\n"))
}

func collectLeaves(e *jsonschema.ValidationError, p *message.Printer) []string {
	if len(e.Causes) == 0 {
		return []string{fmt.Sprintf("  /%s: %s", strings.Join(e.InstanceLocation, "/"), e.ErrorKind.LocalizedString(p))}
	}
	var out []string
	for _, c := range e.Causes {
		out = append(out, collectLeaves(c, p)...)
	}
	return out
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
