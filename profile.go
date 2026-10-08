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
	"regexp"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

var versionFolder = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)*$`)

//go:embed profiles/bronze/profile.json
var bronzeProfileJSON []byte

type rule struct {
	id, title string
	schema    *jsonschema.Schema
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
	titles := map[string]string{}
	parents := map[string]string{}
	paths := map[string]string{bronzeID: bronzeID}
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
		root := filepath.Dir(filepath.Dir(target))
		if versionFolder.MatchString(filepath.Base(filepath.Dir(target))) {
			root = filepath.Dir(root)
		}
		siblings, _ := filepath.Glob(filepath.Join(root, "*", "profile.json"))
		nested, _ := filepath.Glob(filepath.Join(root, "*", "*", "profile.json"))
		versioned := slices.DeleteFunc(nested, func(path string) bool {
			return !versionFolder.MatchString(filepath.Base(filepath.Dir(path)))
		})
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
			docs[id], parents[id], paths[id] = raw, ref, path
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
		var head struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(raw, &head)
		titles[id] = head.Title
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", paths[id], err)
		}
		if err := c.AddResource(id, doc); err != nil {
			return nil, fmt.Errorf("%s: %w", paths[id], err)
		}
	}
	p := &profile{ID: targetID}
	for _, id := range ids {
		sch, err := c.Compile(id)
		if err != nil {
			return nil, fmt.Errorf("profile %s (%s): %w", id, paths[id], err)
		}
		p.rules = append(p.rules, rule{id, titles[id], sch})
	}
	return p, nil
}

// Names for the profiles a crate may declare, by $id.
func (p *profile) titles() map[string]string {
	titles := map[string]string{processRunCrate: "Process Run Crate"}
	for _, r := range p.rules {
		if r.title != "" {
			titles[r.id] = r.title
		}
	}
	return titles
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
	if u, err := url.Parse(head.ID); err != nil || u.Scheme == "" || u.Host == "" || u.Fragment != "" {
		return "", "", fmt.Errorf("$id %q is not an absolute URL such as https://example.org/profiles/name/0.1.0; it is recorded as the crate's conformsTo", head.ID)
	}
	return head.ID, head.Ref, nil
}
