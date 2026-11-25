package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ImperialCollegeLondon/lbf-cli/profiles"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/message/catalog"
)

// A profile version is semantic, as 1, 1.2 or 1.2.3, with or without a leading v.
var versionFolder = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){0,2}$`)

var profileSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

var bronzeProfileJSON = profiles.Bronze

// Where a dataset keeps the schemas it was checked against, beside the published folder.
const profilesDir = "ro-crate-profiles"

const recordPointer = "#/$defs/properties"

type rule struct {
	id, title, parent string
	name, version     string
	raw               []byte
	schema, record    *jsonschema.Schema
	compiler          *jsonschema.Compiler
}

// The schema file a dataset carries for this profile.
func (r rule) file() string { return profilesDir + "/" + r.name + "/" + r.version + "/profile.json" }

// The profiles named and every profile they build on through $ref, bronze first; ID is the last named.
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
			return nil, missingParent(paths[id], parent, bronzeID, skipped)
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
		r := rule{id: id, title: sch.Title, parent: parents[id], raw: docs[id], schema: sch, compiler: c}
		r.name, r.version, _ = nameAndVersion(id)
		if hasRecord(docs[id]) {
			if r.record, err = c.Compile(id + recordPointer); err != nil {
				return nil, fmt.Errorf("profile %s (%s): %w", id, paths[id], err)
			}
		}
		p.rules = append(p.rules, r)
	}
	return p, nil
}

// A profile built on another bronze than this lbf's was written for another version of lbf.
func missingParent(path, parent, bronzeID string, skipped []string) error {
	if name, version, err := nameAndVersion(parent); err == nil && name == "bronze" {
		_, have, _ := nameAndVersion(bronzeID)
		return fmt.Errorf("%s builds on bronze %s, but this lbf checks bronze %s, which lays the crate out differently; give the profile a new version whose $ref is %s", path, version, have, bronzeID)
	}
	err := fmt.Errorf("%s builds on %s, which is neither lbf's bronze profile %s nor a profile beside it", path, parent, bronzeID)
	if len(skipped) > 0 {
		err = fmt.Errorf("%w; these could not be read: %s", err, strings.Join(skipped, ", "))
	}
	return err
}

func hasRecord(raw []byte) bool {
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	_ = json.Unmarshal(raw, &doc)
	_, ok := doc.Defs["properties"]
	return ok
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
			if i >= 0 {
				if !bytes.Equal(merged.rules[i].raw, r.raw) {
					return nil, fmt.Errorf("two copies of %s differ, and a profile version never changes; give the same folder each time, or a new version", r.id)
				}
				continue
			}
			if j := slices.IndexFunc(merged.rules, func(m rule) bool { return m.file() == r.file() }); j >= 0 {
				return nil, fmt.Errorf("%s and %s are both %s %s, so a dataset cannot carry both; give one a new name or version", merged.rules[j].id, r.id, r.name, r.version)
			}
			merged.rules = append(merged.rules, r)
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

// Checks the crate's framed root, and the properties the publisher gave as one record, against each profile, bronze first.
func (p *profile) validate(view map[string]any, record map[string]any) error {
	crate, err := asInstance(view)
	if err != nil {
		return err
	}
	given, err := asInstance(record)
	if err != nil {
		return err
	}
	pr := message.NewPrinter(language.English, message.Catalog(plainWords))
	for _, r := range p.rules {
		var problems []string
		if verr, ok := errors.AsType[*jsonschema.ValidationError](r.schema.Validate(crate)); ok {
			problems = append(problems, p.crateProblems(verr, pr)...)
		}
		if r.record != nil {
			if verr, ok := errors.AsType[*jsonschema.ValidationError](r.record.Validate(given)); ok {
				problems = append(problems, p.recordProblems(verr, pr)...)
			}
		}
		if len(problems) > 0 {
			slices.Sort(problems)
			return fmt.Errorf("dataset does not meet profile %s (%s):\n%s", cmp.Or(r.title, r.id), r.id, strings.Join(slices.Compact(problems), "\n"))
		}
	}
	return nil
}

func asInstance(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(raw))
}

// jsonschema words its messages through x/text, so lbf rewords those a publisher meets.
var plainWords = func() *catalog.Builder {
	b := catalog.NewBuilder()
	for key, msg := range map[string]string{
		"minLength: got %d, want %d":   "is %[1]d characters long, but must be at least %[2]d",
		"maxLength: got %d, want %d":   "is %[1]d characters long, but must be at most %[2]d",
		"%s does not match pattern %s": "%[1]s is not in the form the profile asks for",
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

// Terms of the crate's root lbf sets itself, with what a publisher can do when one fails a profile.
var lbfSets = map[string]string{
	"identifier": "", "datePublished": "", "license": "", "distribution": "", "conformsTo": "",
	"creator": "lbf names whoever signed in, and could not tell who that is; sign in with 'lbf login', or mint the --sas-env file again with 'lbf mint-sas'",
}

// Each problem with the crate said in the terms of the flag that fixes it, with the profile's own title, description and examples.
func (p *profile) crateProblems(e *jsonschema.ValidationError, pr *message.Printer) []string {
	if _, ok := e.ErrorKind.(*kind.Contains); !ok && len(e.Causes) > 0 {
		var out []string
		for _, c := range e.Causes {
			out = append(out, p.crateProblems(c, pr)...)
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
			case len(loc) == 0 && (name == "isBasedOn" || name == "mentions"):
				derived, derivedHint = true, cmp.Or(derivedHint, hint)
			case len(loc) == 0 && rootFlags[name] != "":
				out = append(out, "  missing "+rootFlags[name]+hint)
			case len(loc) == 0 && lbfSets[name] != "":
				out = append(out, fmt.Sprintf("  missing %s: %s%s", name, lbfSets[name], hint))
			case len(loc) == 0:
				out = append(out, fmt.Sprintf("  missing %s, which no flag of lbf's gives, so no dataset can meet this profile; ask whoever looks after it%s", name, hint))
			case len(loc) == 2 && loc[0] == "additionalProperty":
				out = append(out, fmt.Sprintf("  --property %s: the crate records no %s for it, which lbf writes only for what a profile's $defs/properties asks%s", loc[1], name, hint))
			default:
				out = append(out, fmt.Sprintf("  %s: missing %s%s", where(loc), name, hint))
			}
		}
		if derived {
			out = append(out, "  missing --derived-from ID and --instrument, saying what it was made from and with"+derivedHint)
		}
		return out
	}
	msg := plainMessage(e.ErrorKind, pr)
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

// Each problem with the properties given said as the --property that fixes it.
func (p *profile) recordProblems(e *jsonschema.ValidationError, pr *message.Printer) []string {
	if len(e.Causes) > 0 {
		var out []string
		for _, c := range e.Causes {
			out = append(out, p.recordProblems(c, pr)...)
		}
		return out
	}
	loc := e.InstanceLocation
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		var out []string
		for _, name := range k.Missing {
			at := "  missing --property " + name + "=..."
			if len(loc) > 0 {
				at = fmt.Sprintf("  --property %s: missing %s", strings.Join(loc, "/"), name)
			}
			out = append(out, at+p.hint(e.SchemaURL+"/properties/"+name))
		}
		return out
	case *kind.AdditionalProperties:
		var out []string
		for _, name := range k.Properties {
			out = append(out, "  --property "+name+": not a property the profile asks for")
		}
		return out
	}
	msg := plainMessage(e.ErrorKind, pr)
	if len(loc) == 0 {
		return []string{fmt.Sprintf("  the properties: %s%s", msg, p.hint(e.SchemaURL))}
	}
	return []string{fmt.Sprintf("  --property %s: %s%s", strings.Join(loc, "/"), msg, p.hint(e.SchemaURL))}
}

func plainMessage(k jsonschema.ErrorKind, pr *message.Printer) string {
	if got, want, bound := numberBound(k); bound != "" {
		return fmt.Sprintf("is %s, but must be %s %s", got, bound, want)
	}
	if t, ok := k.(*kind.Type); ok {
		var want []string
		for _, w := range t.Want {
			want = append(want, typeWords[w])
		}
		return "is " + typeWords[t.Got] + ", but must be " + strings.Join(want, " or ")
	}
	return k.LocalizedString(pr)
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
		parts = append(parts, "e.g. "+exampleText(s.Examples[0]))
	}
	return strings.Join(parts, "; ")
}

func exampleText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
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
		return "", "", fmt.Errorf("$id %q is not an absolute URL such as https://example.org/profiles/plate-read/0.1.0; it is recorded as the crate's conformsTo", head.ID)
	}
	if _, _, err := nameAndVersion(head.ID); err != nil {
		return "", "", err
	}
	return head.ID, head.Ref, nil
}

// The name and version a profile goes by: the last two segments of its $id.
func nameAndVersion(id string) (name, version string, err error) {
	u, err := url.Parse(id)
	if err == nil && u.RawQuery == "" {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) >= 2 && profileSlug.MatchString(segs[len(segs)-2]) && versionFolder.MatchString(segs[len(segs)-1]) {
			return segs[len(segs)-2], segs[len(segs)-1], nil
		}
	}
	return "", "", fmt.Errorf("$id %s does not end in /<name>/<version>, such as https://example.org/profiles/plate-read/0.1.0; a dataset keeps the schema it was checked against under that name and version", id)
}

// JSON Schema's types, as a publisher would say them.
var typeWords = map[string]string{
	"string": "text", "integer": "a whole number", "number": "a number", "boolean": "true or false",
	"object": "an object", "array": "a list", "null": "empty",
}

// What a profile says one property is: its JSON Schema type, and the unit schema.org's PropertyValue records it in.
type propertyKind struct{ typ, unitCode, unitText string }

// What each property the profiles' records ask for is, so text from a flag is stored as the value it is.
func (p *profile) propertyKinds() map[string]propertyKind {
	kinds := map[string]propertyKind{}
	if p == nil {
		return kinds
	}
	for _, r := range p.rules {
		var doc struct {
			Defs struct {
				Properties struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"properties"`
			} `json:"$defs"`
		}
		if json.Unmarshal(r.raw, &doc) != nil {
			continue
		}
		for name, raw := range doc.Defs.Properties.Properties {
			var s any
			_ = json.Unmarshal(raw, &s)
			k, have := kindOf(s), kinds[name]
			kinds[name] = propertyKind{cmp.Or(k.typ, have.typ), cmp.Or(k.unitCode, have.unitCode), cmp.Or(k.unitText, have.unitText)}
		}
	}
	return kinds
}

func kindOf(s any) propertyKind {
	m, _ := s.(map[string]any)
	var k propertyKind
	k.typ, _ = m["type"].(string)
	k.unitCode, _ = m["x-unitCode"].(string)
	k.unitText, _ = m["x-unitText"].(string)
	subs, _ := m["allOf"].([]any)
	for _, sub := range subs {
		found := kindOf(sub)
		k = propertyKind{cmp.Or(found.typ, k.typ), cmp.Or(found.unitCode, k.unitCode), cmp.Or(found.unitText, k.unitText)}
	}
	return k
}

// Text as the value its type makes it; text that is not one stays text, for the profile to refuse.
func (k propertyKind) value(text string) any {
	switch k.typ {
	case "integer":
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n
		}
	case "number":
		if n, err := strconv.ParseFloat(text, 64); err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
			return n
		}
	case "boolean":
		if text == "true" || text == "false" {
			return text == "true"
		}
	}
	return text
}

// The properties a publisher gave, each as the value its profile types it as.
func (p *profile) record(props map[string]string) map[string]any {
	kinds := p.propertyKinds()
	out := make(map[string]any, len(props))
	for name, text := range props {
		out[name] = kinds[name].value(text)
	}
	return out
}

// A property's value as text, as a publisher gives it.
func valueText(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// A number outside a profile's range, written as given rather than grouped as the English printer would.
func numberBound(k jsonschema.ErrorKind) (got, want, bound string) {
	text := func(r *big.Rat) string { f, _ := r.Float64(); return valueText(f) }
	switch k := k.(type) {
	case *kind.Minimum:
		return text(k.Got), text(k.Want), "at least"
	case *kind.Maximum:
		return text(k.Got), text(k.Want), "at most"
	case *kind.ExclusiveMinimum:
		return text(k.Got), text(k.Want), "more than"
	case *kind.ExclusiveMaximum:
		return text(k.Got), text(k.Want), "less than"
	}
	return "", "", ""
}

// The keywords of JSON Schema 2020-12; a validator ignores any other, so a misspelt one checks nothing.
var schemaKeywords = strings.Fields(`$schema $id $ref $anchor $dynamicRef $dynamicAnchor $vocabulary $comment $defs
	allOf anyOf oneOf not if then else dependentSchemas prefixItems items contains properties patternProperties
	additionalProperties propertyNames unevaluatedItems unevaluatedProperties
	type enum const multipleOf maximum exclusiveMaximum minimum exclusiveMinimum maxLength minLength pattern
	maxItems minItems uniqueItems maxContains minContains maxProperties minProperties required dependentRequired
	title description default deprecated readOnly writeOnly examples format contentEncoding contentMediaType contentSchema`)

// What 'lbf profiles check' finds in a profile that loads: keywords nothing checks, and properties a publisher or form could not give well.
func checkProfile(dir string) (*profile, []string, error) {
	prof, err := loadProfile(dir)
	if err != nil {
		return nil, nil, err
	}
	target := prof.rules[len(prof.rules)-1]
	var doc map[string]any
	if err := json.Unmarshal(target.raw, &doc); err != nil {
		return nil, nil, err
	}
	problems := unknownKeywords(doc, "")
	props, _ := doc["properties"].(map[string]any)
	if ap, ok := props["additionalProperty"].(map[string]any); ok && ap["$ref"] == recordPointer {
		problems = append(problems, "/properties/additionalProperty: the crate's additionalProperty holds schema.org PropertyValues, so $defs/properties cannot check it; leave the $ref out, as lbf checks $defs/properties against the properties given")
	}
	defs, _ := doc["$defs"].(map[string]any)
	record, _ := defs["properties"].(map[string]any)
	required, _ := record["required"].([]any)
	fields, _ := record["properties"].(map[string]any)
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		field, _ := fields[name].(map[string]any)
		say := func(format string, a ...any) { problems = append(problems, "property "+name+fmt.Sprintf(format, a...)) }
		switch {
		case field["type"] == nil && field["enum"] == nil && field["const"] == nil:
			say(` has no type; give "string", "integer", "number" or "boolean", or a list of choices`)
		case field["type"] != nil && !slices.Contains([]any{"string", "integer", "number", "boolean"}, field["type"]):
			say(" has type %s, but a property's value is text, a number or true/false", exampleText(field["type"]))
		}
		for _, want := range [][2]string{{"title", "a form shows as its label"}, {"description", "a form shows as help"}, {"examples", "a form shows as a placeholder"}} {
			if field[want[0]] == nil {
				say(" has no %s, which %s", want[0], want[1])
			}
		}
		if _, ok := field["default"]; ok && slices.Contains(required, any(name)) {
			say(" is required, so a default would answer for whoever publishes; leave it out")
		}
		code, hasCode := field["x-unitCode"]
		_, hasText := field["x-unitText"]
		if s, _ := code.(string); hasCode && !isAbsoluteURL(s) {
			say(" has x-unitCode %s, but it must be a unit's URL, such as http://qudt.org/vocab/unit/NanoM", exampleText(code))
		}
		if hasCode != hasText {
			say(" gives only one of x-unitCode and x-unitText; give both, the unit's URL and how people write it")
		}
		sch, err := target.compiler.Compile(target.id + recordPointer + "/properties/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(name))
		if err != nil {
			continue
		}
		examples, _ := field["examples"].([]any)
		for _, ex := range examples {
			if sch.Validate(ex) != nil {
				say(": example %s does not meet the property's own rules", exampleText(ex))
			}
		}
	}
	return prof, problems, nil
}

func isAbsoluteURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme != "" && u.Host != ""
}

// Keys of a schema that are not JSON Schema keywords, other than x- annotations, with the keyword each was likely meant to be.
func unknownKeywords(s map[string]any, at string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(s)) {
		v := s[k]
		switch {
		case strings.HasPrefix(k, "x-"):
		case !slices.Contains(schemaKeywords, k):
			msg := fmt.Sprintf("%s: %s is not a JSON Schema keyword, so nothing checks it", cmp.Or(at, "/"), k)
			if near := nearestKeyword(k); near != "" {
				msg += "; did you mean " + near + "?"
			}
			out = append(out, msg)
		case slices.Contains([]string{"not", "if", "then", "else", "contains", "additionalProperties", "propertyNames", "unevaluatedItems", "unevaluatedProperties", "items", "contentSchema"}, k):
			if sub, ok := v.(map[string]any); ok {
				out = append(out, unknownKeywords(sub, at+"/"+k)...)
			}
		case slices.Contains([]string{"allOf", "anyOf", "oneOf", "prefixItems"}, k):
			list, _ := v.([]any)
			for i, item := range list {
				if sub, ok := item.(map[string]any); ok {
					out = append(out, unknownKeywords(sub, fmt.Sprintf("%s/%s/%d", at, k, i))...)
				}
			}
		case slices.Contains([]string{"$defs", "properties", "patternProperties", "dependentSchemas"}, k):
			m, _ := v.(map[string]any)
			for _, name := range slices.Sorted(maps.Keys(m)) {
				if sub, ok := m[name].(map[string]any); ok {
					out = append(out, unknownKeywords(sub, at+"/"+k+"/"+name)...)
				}
			}
		}
	}
	return out
}

func nearestKeyword(k string) string {
	best, bestDist := "", 3
	for _, kw := range schemaKeywords {
		if d := editDistance(strings.ToLower(k), strings.ToLower(kw)); d < bestDist {
			best, bestDist = kw, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
