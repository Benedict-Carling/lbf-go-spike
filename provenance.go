package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

const processRunCrate = "https://w3id.org/ro/wfrun/process/0.5"

var (
	//go:embed provenance/schema.json
	provenanceSchemaJSON []byte
	//go:embed provenance/bronze.json
	bronzeProvenanceJSON []byte
	//go:embed profiles/bronze/profile.json
	bronzeProfileJSON []byte
)

type instrument struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
}

// The --provenance file: how a dataset was produced. Its shape is provenance/schema.json.
type provenance struct {
	DerivedFrom string            `json:"derived_from,omitempty"`
	Instruments []instrument      `json:"instruments,omitempty"`
	Properties  map[string]string `json:"properties"`
}

// Without a path, lbf's own minimal bronze provenance is used.
func readProvenance(path string) (provenance, error) {
	raw, label := bronzeProvenanceJSON, "built-in bronze provenance"
	if path != "" {
		var err error
		if raw, err = os.ReadFile(path); err != nil {
			return provenance{}, err
		}
		label = path
	}

	sch, err := compileSchema(nil, mustID(provenanceSchemaJSON))
	if err != nil {
		return provenance{}, err
	}
	if err := validateJSON(sch, raw); err != nil {
		return provenance{}, fmt.Errorf("%s is not a valid provenance file:\n%w", label, err)
	}

	var p provenance
	if err := json.Unmarshal(raw, &p); err != nil {
		return provenance{}, fmt.Errorf("%s: %w", label, err)
	}
	if p.Properties == nil {
		p.Properties = map[string]string{}
	}
	for _, k := range []string{"subscription_name", "subscription_id", "source_path"} {
		if _, ok := p.Properties[k]; ok {
			return provenance{}, fmt.Errorf("%s: property %q is set by lbf and cannot be supplied", label, k)
		}
	}
	return p, nil
}

type profile struct {
	ID     string
	schema *jsonschema.Schema
}

// Loads <dir>/profile.json, or lbf's bronze profile when dir is empty. Sibling profiles and the
// bronze profile are registered by $id, so `$ref` between them resolves without the network.
func loadProfile(dir string) (*profile, error) {
	if dir == "" {
		sch, err := compileSchema(nil, mustID(bronzeProfileJSON))
		if err != nil {
			return nil, err
		}
		return &profile{ID: mustID(bronzeProfileJSON), schema: sch}, nil
	}

	target := filepath.Join(dir, "profile.json")
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		target = dir
	}
	siblings, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(target)), "*", "profile.json"))

	docs := map[string][]byte{}
	targetID := ""
	for _, path := range append(siblings, target) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		id, err := schemaID(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		docs[id] = raw
		if path == target {
			targetID = id
		}
	}

	sch, err := compileSchema(docs, targetID)
	if err != nil {
		return nil, fmt.Errorf("profile %s: %w", target, err)
	}
	return &profile{ID: targetID, schema: sch}, nil
}

// The document a profile validates: what the caller supplied plus the files actually found.
func profileView(id string, prov provenance, files []localFile) map[string]any {
	view := map[string]any{"identifier": id, "properties": prov.Properties}
	if prov.DerivedFrom != "" {
		view["derived_from"] = prov.DerivedFrom
		view["instruments"] = prov.Instruments
	}
	fs := make([]map[string]any, len(files))
	for i, f := range files {
		fs[i] = map[string]any{"path": f.Rel, "size": f.Size}
	}
	view["files"] = fs
	return view
}

func (p *profile) validate(view map[string]any) error {
	raw, err := json.Marshal(view)
	if err != nil {
		return err
	}
	if err := validateJSON(p.schema, raw); err != nil {
		return fmt.Errorf("dataset does not meet profile %s:\n%w", p.ID, err)
	}
	return nil
}

// The built-in documents are always registered, so any profile can $ref them.
func compileSchema(docs map[string][]byte, id string) (*jsonschema.Schema, error) {
	all := map[string][]byte{
		mustID(provenanceSchemaJSON): provenanceSchemaJSON,
		mustID(bronzeProfileJSON):    bronzeProfileJSON,
	}
	for k, v := range docs {
		all[k] = v
	}

	c := jsonschema.NewCompiler()
	c.AssertFormat()
	for docID, raw := range all {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", docID, err)
		}
		if err := c.AddResource(docID, doc); err != nil {
			return nil, fmt.Errorf("%s: %w", docID, err)
		}
	}
	return c.Compile(id)
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

func schemaID(raw []byte) (string, error) {
	var head struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return "", err
	}
	if head.ID == "" {
		return "", errors.New("no $id; it is recorded as the crate's conformsTo")
	}
	return head.ID, nil
}

func mustID(raw []byte) string {
	id, err := schemaID(raw)
	if err != nil {
		panic(err)
	}
	return id
}
