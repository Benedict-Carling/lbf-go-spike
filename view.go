package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/piprate/json-gold/ld"
)

const (
	roCrateContext   = "https://w3id.org/ro/crate/1.3/context"
	roCrate12Context = "https://w3id.org/ro/crate/1.2/context"
)

//go:embed ro-crate-1.3-context.json
var roCrateContextJSON []byte

//go:embed profiles/bronze/frame.json
var bronzeFrameJSON []byte

// Relative @ids need a base to frame against; compaction turns them back into the crate's own.
const frameBase = "https://crate.invalid/"

// lbf frames only crates lbf wrote, so their contexts are compiled in and nothing is fetched.
type offlineLoader struct{ context any }

// 1.3 changed only the Bioschemas workflow terms of 1.2, which lbf's crates never used.
func (l offlineLoader) LoadDocument(u string) (*ld.RemoteDocument, error) {
	if u != roCrateContext && u != roCrate12Context {
		return nil, ld.NewJsonLdError(ld.LoadingDocumentFailed, fmt.Sprintf("lbf knows only the contexts of the crates it writes, not %s", u))
	}
	return &ld.RemoteDocument{DocumentURL: u, Document: l.context}, nil
}

var jsonld = sync.OnceValues(func() (*ld.JsonLdOptions, error) {
	context, err := ld.DocumentFromReader(bytes.NewReader(roCrateContextJSON))
	if err != nil {
		return nil, fmt.Errorf("the compiled-in RO-Crate context: %w", err)
	}
	opts := ld.NewJsonLdOptions(frameBase)
	opts.ProcessingMode = ld.JsonLd_1_1
	opts.DocumentLoader = offlineLoader{context}
	return opts, nil
})

// What profiles validate: the crate's root with everything it refers to embedded, laid out by bronze's frame.
func crateView(crate []byte) (map[string]any, error) {
	var frame map[string]any
	if err := json.Unmarshal(bronzeFrameJSON, &frame); err != nil {
		return nil, err
	}
	return frameCrate(crate, frame)
}

// How lbf reads a crate back, whichever version wrote it: earlier ones record the parent as wasDerivedFrom and do not mention the run.
var statementFrame = map[string]any{
	"@context": []any{roCrateContext, map[string]any{
		"additionalProperty": map[string]any{"@id": "http://schema.org/additionalProperty", "@container": "@index", "@index": "name"},
		"isBasedOn":          map[string]any{"@id": "http://schema.org/isBasedOn", "@container": "@set"},
		"wasDerivedFrom":     map[string]any{"@id": "http://www.w3.org/ns/prov#wasDerivedFrom", "@container": "@set"},
		"instrument":         map[string]any{"@id": "http://schema.org/instrument", "@container": "@set"},
		"conformsTo":         map[string]any{"@id": "http://purl.org/dc/terms/conformsTo", "@container": "@set"},
		"hasResource":        map[string]any{"@id": "http://www.w3.org/ns/dx/prof/hasResource", "@container": "@set"},
	}},
	"@id":      "./",
	"@reverse": map[string]any{"result": map[string]any{}},
}

func frameCrate(crate []byte, frame map[string]any) (map[string]any, error) {
	opts, err := jsonld()
	if err != nil {
		return nil, err
	}
	doc, err := ld.DocumentFromReader(bytes.NewReader(crate))
	if err != nil {
		return nil, err
	}
	// Framing time grows with the square of the entities, so files, which refer to nothing, join the view after it.
	parts, doc := setFilesAside(doc)
	fopts := opts.Copy()
	fopts.Embed = ld.EmbedAlways
	fopts.OmitGraph = true
	view, err := ld.NewJsonLdProcessor().Frame(doc, frame, fopts)
	if err != nil {
		return nil, fmt.Errorf("framing the crate: %w", err)
	}
	delete(view, "@context")
	if len(parts) > 0 {
		view["hasPart"] = parts
	}
	return view, nil
}

func setFilesAside(doc any) ([]any, any) {
	d, _ := doc.(map[string]any)
	graph, _ := d["@graph"].([]any)
	byID := map[string]map[string]any{}
	var root map[string]any
	for _, e := range graph {
		m, _ := e.(map[string]any)
		id, _ := m["@id"].(string)
		byID[id] = m
		if id == "./" {
			root = m
		}
	}
	if root == nil {
		return nil, doc
	}
	var parts []any
	aside := map[string]bool{}
	for _, p := range asList(root["hasPart"]) {
		id, _ := p.(map[string]any)["@id"].(string)
		if e, ok := byID[id]; ok && e["@type"] == "File" {
			parts = append(parts, e)
			aside[id] = true
		}
	}
	if len(parts) != len(asList(root["hasPart"])) {
		return nil, doc
	}
	delete(root, "hasPart")
	rest := slices.DeleteFunc(slices.Clone(graph), func(e any) bool {
		id, _ := e.(map[string]any)["@id"].(string)
		return aside[id]
	})
	return parts, map[string]any{"@context": d["@context"], "@graph": rest}
}

// What a crate lbf wrote states about its dataset, leaving out lbf's own properties, and the schema each declared profile carries by $id.
func crateStatement(crate []byte) (provenance, map[string]string, error) {
	view, err := frameCrate(crate, statementFrame)
	if err != nil {
		return provenance{}, nil, err
	}
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	str := func(v any, key string) string { s, _ := obj(v)[key].(string); return s }

	p := provenance{Name: str(view, "name"), Description: str(view, "description"), Properties: map[string]string{}}
	for name, pv := range obj(view["additionalProperty"]) {
		if !slices.Contains(lbfProperties, name) {
			p.Properties[name] = str(pv, "value")
		}
	}
	for _, source := range slices.Concat(asList(view["isBasedOn"]), asList(view["wasDerivedFrom"])) {
		p.DerivedFrom = str(source, "identifier")
	}
	for _, run := range asList(obj(view["@reverse"])["result"]) {
		for _, in := range asList(obj(run)["instrument"]) {
			p.Instruments = append(p.Instruments, instrument{str(in, "name"), str(in, "version"), str(in, "url")})
		}
	}
	schemas := map[string]string{}
	for _, prof := range asList(view["conformsTo"]) {
		schemas[str(prof, "@id")] = ""
		for _, res := range asList(obj(prof)["hasResource"]) {
			if artifact := obj(res)["hasArtifact"]; str(artifact, "encodingFormat") == "application/schema+json" {
				schemas[str(prof, "@id")] = str(artifact, "text")
			}
		}
	}
	return p, schemas, nil
}
