package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

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
