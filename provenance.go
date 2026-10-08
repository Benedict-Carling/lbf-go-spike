package main

import (
	"bytes"
	"cmp"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
)

var lbfProperties = []string{"subscription_name", "subscription_id", "source_path"}

type instrument struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
}

// The --provenance file: what the publisher states about a dataset.
type provenance struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	DerivedFrom string            `json:"derived_from,omitempty"`
	Instruments []instrument      `json:"instruments,omitempty"`
	Properties  map[string]string `json:"properties"`
}

// Where publish takes its provenance from: a --provenance file, the flags that spell it out, or both.
type provenanceFlags struct {
	file, derivedFrom, properties string
	name, description             string
	instruments                   []instrument
	props                         []string
}

func (f provenanceFlags) load() (provenance, error) {
	p := provenance{DerivedFrom: f.derivedFrom, Instruments: f.instruments, Properties: map[string]string{}}
	if f.file != "" {
		if f.derivedFrom != "" || len(f.instruments) > 0 || f.properties != "" {
			return provenance{}, errors.New("--provenance cannot be combined with --derived-from, --instrument or --properties")
		}
		var err error
		if p, err = readProvenance(f.file); err != nil {
			return provenance{}, err
		}
	}
	if f.properties != "" {
		err := decodeStrict(f.properties, &p.Properties)
		if err == nil && p.Properties == nil {
			err = errors.New("want a JSON object of property names and values")
		}
		if err != nil {
			return provenance{}, fmt.Errorf("%s is not a valid properties file: %w", f.properties, err)
		}
	}
	for _, kv := range f.props {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			return provenance{}, fmt.Errorf("--property %q is not NAME=VALUE", kv)
		}
		if _, dup := p.Properties[name]; dup {
			return provenance{}, fmt.Errorf("property %q is given twice", name)
		}
		p.Properties[name] = value
	}
	for _, field := range []struct {
		flag, value string
		into        *string
	}{{"name", f.name, &p.Name}, {"description", f.description, &p.Description}} {
		if field.value != "" && *field.into != "" {
			return provenance{}, fmt.Errorf("--%s is also given in %s", field.flag, f.file)
		}
		*field.into = cmp.Or(*field.into, field.value)
	}
	if err := p.check(); err != nil {
		return provenance{}, err
	}
	return p, nil
}

// Instruments are a set in the crate, so neither their order nor a repeat is part of what was stated.
func (p provenance) sameAs(q provenance) bool {
	set := func(ins []instrument) []instrument {
		ins = slices.Clone(ins)
		slices.SortFunc(ins, func(a, b instrument) int {
			return cmp.Or(cmp.Compare(a.URL, b.URL), cmp.Compare(a.Name, b.Name), cmp.Compare(a.Version, b.Version))
		})
		return slices.Compact(ins)
	}
	return p.Name == q.Name && p.Description == q.Description && p.DerivedFrom == q.DerivedFrom &&
		slices.Equal(set(p.Instruments), set(q.Instruments)) && maps.Equal(p.Properties, q.Properties)
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
	err = jsonv2.Unmarshal(bytes.TrimPrefix(raw, []byte("\uFEFF")), v, jsonv2.RejectUnknownMembers(true))
	if serr, ok := errors.AsType[*jsonv2.SemanticError](err); ok && serr.Err == jsonv2.ErrUnknownName {
		field := serr.JSONPointer.LastToken()
		if at := serr.JSONPointer.Parent(); at != "" {
			return fmt.Errorf("unknown field %q in %s", field, at)
		}
		return fmt.Errorf("unknown field %q", field)
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
	if p.DerivedFrom != "" && !mintedID.MatchString(p.DerivedFrom) {
		return fmt.Errorf("derived_from %q is not a dataset ID such as 20261001-fancy-dassie-eadb", p.DerivedFrom)
	}
	byURL := map[string]int{}
	for i, in := range p.Instruments {
		if in.Name == "" || in.Version == "" || in.URL == "" {
			return fmt.Errorf("instruments[%d] needs a name, version and url", i)
		}
		if u, err := url.Parse(in.URL); err != nil || !u.IsAbs() {
			return fmt.Errorf("instruments[%d] url %q is not an absolute URL", i, in.URL)
		}
		if j, ok := byURL[in.URL]; ok && p.Instruments[j] != in {
			return fmt.Errorf("instruments[%d] (%s %s) and instruments[%d] (%s %s) share the url %s, which the crate uses to tell tools apart; give each its own url",
				j, p.Instruments[j].Name, p.Instruments[j].Version, i, in.Name, in.Version, in.URL)
		}
		byURL[in.URL] = i
	}
	for _, k := range lbfProperties {
		if _, ok := p.Properties[k]; ok {
			return fmt.Errorf("property %q is set by lbf and cannot be supplied", k)
		}
	}
	for k := range p.Properties {
		if k == "uploader" || k == "blob-location" || k == "run" || strings.HasPrefix(k, "source-") || strings.HasPrefix(k, "profile-") || strings.HasPrefix(k, "@") {
			return fmt.Errorf("property %q would clash with an entity or keyword in the crate; choose another name", k)
		}
	}
	return nil
}
