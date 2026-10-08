package main

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/json"
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
	"golang.org/x/text/message/catalog"
)

// A profile version is semantic, as 1, 1.2 or 1.2.3, with or without a leading v.
var versionFolder = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){0,2}$`)

var profileSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

//go:embed profiles/bronze/profile.json
var bronzeProfileJSON []byte

type rule struct {
	id, title, parent string
	raw               []byte
	schema            *jsonschema.Schema
	compiler          *jsonschema.Compiler
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
		for _, path := range append(profilesBeside(target), target) {
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
		p.rules = append(p.rules, rule{id, sch.Title, parents[id], docs[id], sch, c})
	}
	return p, nil
}

// The profiles a profile.json may build on without the network: profiles/<name>/profile.json or, versioned, profiles/<name>/<version>/profile.json.
func profilesBeside(target string) []string {
	root := filepath.Dir(filepath.Dir(target))
	if versionFolder.MatchString(filepath.Base(filepath.Dir(target))) {
		root = filepath.Dir(root)
	}
	siblings, _ := filepath.Glob(filepath.Join(root, "*", "profile.json"))
	nested, _ := filepath.Glob(filepath.Join(root, "*", "*", "profile.json"))
	versioned := slices.DeleteFunc(nested, func(path string) bool {
		return !versionFolder.MatchString(filepath.Base(filepath.Dir(path)))
	})
	return slices.Concat(siblings, versioned)
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
				return nil, fmt.Errorf("two copies of %s differ, and a profile version never changes; give the --profile that is published, or a new version", r.id)
			}
		}
		merged.ID = p.ID
	}
	return merged, nil
}

// The profile named, as opposed to those it builds on.
func (p *profile) chosen() rule {
	return p.rules[slices.IndexFunc(p.rules, func(r rule) bool { return r.id == p.ID })]
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
			return fmt.Errorf("dataset does not meet profile %s (%s):\n%w", cmp.Or(r.title, r.id), r.id, err)
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
	problems := p.collectLeaves(verr, message.NewPrinter(language.English, message.Catalog(plainWords)))
	slices.Sort(problems)
	return errors.New(strings.Join(slices.Compact(problems), "\n"))
}

// jsonschema words its messages through x/text, so lbf rewords those a publisher meets.
var plainWords = func() *catalog.Builder {
	b := catalog.NewBuilder()
	for key, msg := range map[string]string{
		"minLength: got %d, want %d":   "is %[1]d characters long, but must be at least %[2]d",
		"maxLength: got %d, want %d":   "is %[1]d characters long, but must be at most %[2]d",
		"%s does not match pattern %s": "%[1]s is not in the form the profile asks for",
		"got %s, want %s":              "is a %[1]s, but must be a %[2]s",
		"minItems: got %d, want %d":    "has %[1]d, but must have at least %[2]d",
	} {
		b.SetString(language.English, key, msg)
	}
	return b
}()

// How a publisher gives each term of the crate's root that is theirs to give.
var rootFlags = map[string]string{
	"name": "--name", "description": "--description", "additionalProperty": "--property",
	"isBasedOn": "--derived-from", "mentions": "--instrument", "hasPart": "files",
}

// Something a profile asks of a publisher: the flag that gives it, whether it must, and the profile's own words for it.
type ask struct{ flag, need, hint string }

// Every flag the profile asks for, the chosen profile's first, then those it builds on.
func (p *profile) asks() []ask {
	var asks []ask
	seen := map[string]bool{}
	add := func(a ask) {
		if !seen[a.flag] {
			seen[a.flag] = true
			asks = append(asks, a)
		}
	}
	derived := false
	for i, r := range slices.Backward(p.rules) {
		for _, s := range withAllOf(r.schema) {
			if slices.Contains(s.Required, "isBasedOn") || slices.Contains(s.Required, "mentions") {
				derived = true
			}
			for _, props := range withAllOf(s.Properties["additionalProperty"]) {
				for _, n := range slices.Concat(props.Required, slices.Sorted(maps.Keys(props.Properties))) {
					need := "optional"
					if slices.Contains(props.Required, n) {
						need = "required"
					}
					add(ask{"--property " + n + "=...", need, annotations(props.Properties[n])})
				}
			}
			if h := annotations(s.Properties["hasPart"]); h != "" {
				add(ask{rootFlags["hasPart"], "required", h})
			}
			if i == 0 {
				continue
			}
			for _, f := range []string{"name", "description"} {
				if sub := s.Properties[f]; sub != nil {
					add(ask{"--" + f + " ...", "required", cmp.Or(annotations(sub), "lbf's own does not meet it")})
				}
			}
			for _, n := range s.Required {
				_, flagged := rootFlags[n]
				_, set := lbfSets[n]
				if !flagged && !set {
					add(ask{n, "required", "no flag of lbf's gives it, so no dataset can meet this profile"})
				}
			}
		}
	}
	if derived {
		add(ask{"--derived-from ID --instrument ...", "required", "what it was made from and with"})
	}
	for _, f := range []string{"name", "description"} {
		add(ask{"--" + f + " ...", "optional", "lbf fills it in if left out"})
	}
	return asks
}

// A schema and those it combines with allOf, which profiles use to group rules.
func withAllOf(s *jsonschema.Schema) []*jsonschema.Schema {
	if s == nil {
		return nil
	}
	out := []*jsonschema.Schema{s}
	for _, sub := range s.AllOf {
		out = append(out, withAllOf(sub)...)
	}
	return out
}

// Terms of the crate's root lbf sets itself, with what a publisher can do when one fails a profile.
var lbfSets = map[string]string{
	"identifier": "", "datePublished": "", "license": "", "distribution": "", "conformsTo": "",
	"creator": "lbf names whoever signed in, and could not tell who that is; sign in with 'lbf login', or mint the --sas-env file again with 'lbf mint-sas'",
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
			case len(loc) == 0 && rootFlags[name] != "":
				out = append(out, "  missing "+rootFlags[name]+hint)
			case len(loc) == 0 && lbfSets[name] != "":
				out = append(out, fmt.Sprintf("  missing %s: %s%s", name, lbfSets[name], hint))
			case len(loc) == 0:
				out = append(out, fmt.Sprintf("  missing %s, which no flag of lbf's gives, so no dataset can meet this profile; ask whoever looks after it%s", name, hint))
			default:
				out = append(out, fmt.Sprintf("  %s: missing %s%s", where(loc), name, hint))
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
			"hasPart":  "no file is what the profile asks for",
			"mentions": "the --instrument flags do not give what the profile asks for",
		}[strings.Join(loc, "/")]
		msg = cmp.Or(msg, "nothing here is what the profile asks for")
	}
	if len(loc) > 0 {
		if _, ok := lbfSets[loc[0]]; ok {
			msg += ", but lbf sets this itself; ask whoever looks after the profile"
		}
	}
	if len(loc) >= 2 && loc[0] == "additionalProperty" {
		return []string{fmt.Sprintf("  --property %s: %s%s", loc[1], msg, p.hint(strings.TrimSuffix(e.SchemaURL, "/properties/value")))}
	}
	return []string{fmt.Sprintf("  %s: %s%s", where(loc), msg, p.hint(e.SchemaURL))}
}

// Where in the crate a problem is, by the flag that sets it when there is one.
func where(loc []string) string {
	if len(loc) > 0 && rootFlags[loc[0]] != "" {
		return strings.Join(append([]string{rootFlags[loc[0]]}, loc[1:]...), "/")
	}
	return "/" + strings.Join(loc, "/")
}

func (p *profile) hint(schemaURL string) string {
	if s := annotations(p.schemaAt(schemaURL)); s != "" {
		return "\n      " + s
	}
	return ""
}

// The compiled schema at a location in one of the profile's rules, if there is one.
func (p *profile) schemaAt(loc string) *jsonschema.Schema {
	base, _, _ := strings.Cut(loc, "#")
	for _, r := range p.rules {
		if r.id == base {
			s, _ := r.compiler.Compile(loc)
			return s
		}
	}
	return nil
}

// The title, description and first example a profile gives a schema.
func annotations(s *jsonschema.Schema) string {
	if s == nil {
		return ""
	}
	var parts []string
	for _, t := range []string{s.Title, s.Description} {
		if t != "" {
			parts = append(parts, t)
		}
	}
	if len(s.Examples) > 0 {
		parts = append(parts, fmt.Sprintf("e.g. %v", s.Examples[0]))
	}
	return strings.Join(parts, "; ")
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

// A published profile: its name, and its version unless the latest is meant.
type published struct{ name, version string }

// The name and version a profile is published under: the last two segments of its $id.
func publishedAs(id string) (published, error) {
	u, err := url.Parse(id)
	if err == nil {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) >= 2 && profileSlug.MatchString(segs[len(segs)-2]) && versionFolder.MatchString(segs[len(segs)-1]) {
			return published{segs[len(segs)-2], segs[len(segs)-1]}, nil
		}
	}
	return published{}, fmt.Errorf("$id %s does not end in /<name>/<version>, such as https://w3id.org/lbf/profiles/plate-read/0.1.0, so it cannot be published", id)
}
