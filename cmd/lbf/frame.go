package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ImperialCollegeLondon/lbf-cli/profiles"

	"github.com/piprate/json-gold/ld"
)

const (
	roCrateContext   = "https://w3id.org/ro/crate/1.3/context"
	roCrate12Context = "https://w3id.org/ro/crate/1.2/context"
)

//go:embed ro-crate-1.3-context.json
var roCrateContextJSON []byte

// Relative @ids need a base to frame against; compaction turns them back into the crate's own.
const frameBase = "https://crate.invalid/"

// lbf frames only crates lbf wrote, so their contexts are compiled in and nothing is fetched.
type offlineLoader struct{ context any }

// 1.3 changed only the Bioschemas workflow terms of 1.2, which lbf's crates never used.
func (l offlineLoader) LoadDocument(u string) (*ld.RemoteDocument, error) {
	if u != roCrateContext && u != roCrate12Context {
		return nil, ld.NewJsonLdError(ld.LoadingDocumentFailed, fmt.Sprintf("the crate uses the context %s, but lbf reads only the RO-Crate 1.2 and 1.3 contexts of the crates it writes; this crate was not written by lbf", u))
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

// What profiles check: the crate's root as bronze's frame lays it out, with the published files, not the schemas, as hasPart.
func framedRoot(crate []byte) (map[string]any, error) {
	var frame, doc map[string]any
	if err := json.Unmarshal(profiles.BronzeFrame, &frame); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(crate, &doc); err != nil {
		return nil, err
	}
	graph, _ := doc["@graph"].([]any)
	byID := map[string]map[string]any{}
	for _, e := range graph {
		if m, ok := e.(map[string]any); ok {
			byID[refID(m)] = m
		}
	}
	root := byID["./"]
	var parts []any
	aside := map[string]bool{}
	for _, part := range asList(root["hasPart"]) {
		id := refID(part)
		if !isProfileFile(id) {
			parts = append(parts, byID[id])
			aside[id] = true
		}
	}
	// Framing time grows with the square of the entities, so files, which refer to nothing, join the view after it.
	delete(root, "hasPart")
	doc["@graph"] = slices.DeleteFunc(graph, func(e any) bool { return aside[refID(e)] })
	view, err := frameCrate(doc, frame)
	if err != nil {
		return nil, err
	}
	view["hasPart"] = parts
	return view, nil
}

func isProfileFile(rel string) bool { return strings.HasPrefix(rel, profilesDir+"/") }

func frameCrate(doc map[string]any, frame map[string]any) (map[string]any, error) {
	opts, err := jsonld()
	if err != nil {
		return nil, err
	}
	fopts := opts.Copy()
	fopts.Embed = ld.EmbedAlways
	fopts.OmitGraph = true
	view, err := ld.NewJsonLdProcessor().Frame(ld.CloneDocument(doc), frame, fopts)
	if err != nil {
		return nil, fmt.Errorf("framing the crate: %w", err)
	}
	delete(view, "@context")
	return view, nil
}
