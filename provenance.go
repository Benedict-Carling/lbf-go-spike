package main

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

const processRunCrate = "https://w3id.org/ro/wfrun/process/0.6"

var versionFolder = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)*$`)

var lbfProperties = []string{"subscription_name", "subscription_id", "source_path"}

//go:embed profiles/bronze/profile.json
var bronzeProfileJSON []byte

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

type rule struct {
	id, title, parent string
	raw               []byte
	schema            *jsonschema.Schema
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
	var skipped []string
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
				skipped = append(skipped, fmt.Sprintf("%s (%v)", path, err))
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if id == bronzeID && path != target {
				continue
			}
			if id == bronzeID {
				return nil, fmt.Errorf("%s: $id %s is lbf's bronze profile, which only lbf defines", path, id)
			}
			if have, ok := docs[id]; ok && paths[id] != path && !bytes.Equal(have, raw) {
				return nil, fmt.Errorf("%s and %s both have the $id %s but differ; a profile version never changes, so give one a new version", paths[id], path, id)
			}
			docs[id], parents[id], paths[id] = raw, ref, path
			if path == target {
				targetID = id
			}
		}
	}

	var chain []string
	for id := targetID; id != "" && id != bronzeID && !slices.Contains(chain, id); id = parents[id] {
		if parent := parents[id]; parent != "" && docs[parent] == nil {
			err := fmt.Errorf("%s builds on %s, which is neither lbf's bronze profile %s nor a profile beside it", paths[id], parent, bronzeID)
			if len(skipped) > 0 {
				err = fmt.Errorf("%w; these could not be read: %s", err, strings.Join(skipped, ", "))
			}
			return nil, err
		}
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
		p.rules = append(p.rules, rule{id, titles[id], parents[id], docs[id], sch})
	}
	return p, nil
}

// Every profile named and those each builds on, once each and bronze first; ID is the last named.
func loadProfiles(dirs []string) (*profile, error) {
	merged, err := loadProfile("")
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		p, err := loadProfile(dir)
		if err != nil {
			return nil, err
		}
		for _, r := range p.rules {
			i := slices.IndexFunc(merged.rules, func(m rule) bool { return m.id == r.id })
			if i < 0 {
				merged.rules = append(merged.rules, r)
			} else if !bytes.Equal(merged.rules[i].raw, r.raw) {
				return nil, fmt.Errorf("two copies of %s differ; a profile version never changes", r.id)
			}
		}
		merged.ID = p.ID
	}
	return merged, nil
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
		if err := p.validateJSON(r.schema, inst); err != nil {
			return fmt.Errorf("dataset does not meet profile %s:\n%w", r.id, err)
		}
	}
	return nil
}

func (p *profile) validateJSON(sch *jsonschema.Schema, inst any) error {
	err := sch.Validate(inst)
	verr, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return err
	}
	problems := p.collectLeaves(verr, message.NewPrinter(language.English))
	slices.Sort(problems)
	return errors.New(strings.Join(slices.Compact(problems), "\n"))
}

// Each problem is said in the terms of the flag that fixes it, with the profile's own title, description and examples.
func (p *profile) collectLeaves(e *jsonschema.ValidationError, pr *message.Printer) []string {
	if _, ok := e.ErrorKind.(*kind.Contains); !ok && len(e.Causes) > 0 {
		var out []string
		for _, c := range e.Causes {
			out = append(out, p.collectLeaves(c, pr)...)
		}
		return out
	}
	loc := e.InstanceLocation
	at := "/" + strings.Join(loc, "/")
	if req, ok := e.ErrorKind.(*kind.Required); ok {
		var out []string
		derived, derivedHint := false, ""
		for _, name := range req.Missing {
			hint := p.hint(e.SchemaURL + "/properties/" + name)
			switch {
			case len(loc) == 1 && loc[0] == "additionalProperty":
				out = append(out, "  missing --property "+name+"=..."+hint)
			case len(loc) == 0 && (name == "isBasedOn" || name == "mentions"):
				derived, derivedHint = true, cmp.Or(derivedHint, hint)
			default:
				out = append(out, fmt.Sprintf("  %s: missing %s%s", at, name, hint))
			}
		}
		if derived {
			out = append(out, "  missing --derived-from ID and --instrument, saying what it was made from and with"+derivedHint)
		}
		return out
	}
	msg := e.ErrorKind.LocalizedString(pr)
	if _, ok := e.ErrorKind.(*kind.Contains); ok {
		msg = map[string]string{
			"/hasPart":  "no file is what the profile asks for",
			"/mentions": "the --instrument flags do not give what the profile asks for",
		}[at]
		msg = cmp.Or(msg, "nothing here is what the profile asks for")
	}
	if len(loc) >= 2 && loc[0] == "additionalProperty" {
		return []string{fmt.Sprintf("  --property %s: %s%s", loc[1], msg, p.hint(strings.TrimSuffix(e.SchemaURL, "/properties/value")))}
	}
	return []string{fmt.Sprintf("  %s: %s%s", at, msg, p.hint(e.SchemaURL))}
}

// The title, description and examples a profile gives at a schema location, if any.
func (p *profile) hint(schemaURL string) string {
	base, pointer, _ := strings.Cut(schemaURL, "#")
	var doc any
	for _, r := range p.rules {
		if r.id == base {
			_ = json.Unmarshal(r.raw, &doc)
		}
	}
	for tok := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
		m, ok := doc.(map[string]any)
		if !ok || tok == "" {
			break
		}
		doc = m[strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")]
	}
	m, _ := doc.(map[string]any)
	var parts []string
	for _, k := range []string{"title", "description"} {
		if s, ok := m[k].(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	if ex, ok := m["examples"].([]any); ok && len(ex) > 0 {
		parts = append(parts, fmt.Sprintf("e.g. %v", ex[0]))
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n      " + strings.Join(parts, "; ")
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
