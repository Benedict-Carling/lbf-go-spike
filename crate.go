package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const processRunCrate = "https://w3id.org/ro/wfrun/process/0.6"

const licenseURL = "https://rightsstatements.org/vocab/InC/1.0/"

// A fixed table, not the OS's, so a crate is the same wherever it is written.
var mediaTypes = map[string]string{
	".csv": "text/csv", ".tsv": "text/tab-separated-values", ".txt": "text/plain", ".md": "text/markdown",
	".json": "application/json", ".xml": "application/xml", ".pdf": "application/pdf",
	".tif": "image/tiff", ".tiff": "image/tiff", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".zip": "application/zip", ".gz": "application/gzip", ".parquet": "application/vnd.apache.parquet",
	".h5": "application/x-hdf5", ".hdf5": "application/x-hdf5", ".fcs": "application/vnd.isac.fcs",
}

type entity map[string]any

func ref(id string) entity { return entity{"@id": id} }

// RO-Crate 1.3 writes one value on its own, not as a list of one.
func refs(ids []entity) any {
	if len(ids) == 1 {
		return ids[0]
	}
	return ids
}

const roCrateSpec = "https://w3id.org/ro/crate/1.3"

var profileVersion = regexp.MustCompile(`/v?[0-9]+(\.[0-9]+)*$`)

// What lbf names a dataset and says of it when the publisher does not.
func defaultName(source string) string { return filepath.Base(source) }

func defaultDescription(source string, files int, derivedFrom string) string {
	noun := "files"
	if files == 1 {
		noun = "file"
	}
	d := fmt.Sprintf("Dataset of %d %s from %s", files, noun, filepath.Base(source))
	if derivedFrom != "" {
		d += ", derived from " + derivedFrom
	}
	return d
}

// Checked here rather than in preparePublication because the uploader is only known once signed in.
func (p publication) validate(t target) (time.Time, error) {
	published := time.Now()
	if err := p.Profile.validate(p.view(t, published)); err != nil {
		return time.Time{}, err
	}
	logf("[profile] meets %s\n", strings.Join(p.Profile.ids(), ", "))
	return published, nil
}

// What profiles check: the dataset's files beside what its crate records.
func (p publication) view(t target, published time.Time) map[string]any {
	name := filepath.Base(p.Source)
	files := make([]map[string]any, len(p.Files))
	for i, f := range p.Files {
		files[i] = map[string]any{"path": strings.TrimPrefix(f.Rel, name+"/"), "size": f.Size}
	}
	crate := map[string]any{
		"identifier":         p.ID,
		"datePublished":      timestamp(published),
		"conformsTo":         p.conformsTo(),
		"additionalProperty": p.Provenance.Properties,
	}
	if user := strings.TrimSpace(t.User); user != "" {
		crate["creator"] = user
	}
	if p.Provenance.DerivedFrom != "" {
		crate["wasDerivedFrom"] = p.Provenance.DerivedFrom
		crate["instrument"] = p.Provenance.Instruments
	}
	return map[string]any{
		"data":  map[string]any{"name": name, "files": files},
		"crate": crate,
	}
}

func (p publication) crate(t target) ([]byte, error) {
	published, err := p.validate(t)
	if err != nil {
		return nil, err
	}
	return p.buildCrate(t, published)
}

// RO-Crate 1.3, flattened; encoding/json sorts keys.
func (p publication) buildCrate(t target, published time.Time) ([]byte, error) {
	props := p.properties(t)
	parts := make([]entity, len(p.Files))
	for i, f := range p.Files {
		parts[i] = ref(fileID(f.Rel))
	}
	root := entity{
		"@id":                "./",
		"@type":              "Dataset",
		"identifier":         p.ID,
		"name":               cmp.Or(p.Provenance.Name, defaultName(p.Source)),
		"description":        cmp.Or(p.Provenance.Description, defaultDescription(p.Source, len(p.Files), p.Provenance.DerivedFrom)),
		"datePublished":      timestamp(published),
		"license":            ref(licenseURL),
		"distribution":       ref("#blob-location"),
		"additionalProperty": refs(refsTo(props)),
		"hasPart":            refs(parts),
	}
	graph := []entity{
		root,
		{"@id": crateName, "@type": "CreativeWork", "about": ref("./"), "conformsTo": ref(roCrateSpec)},
		{"@id": "#blob-location", "@type": "DataDownload", "name": "Where " + p.ID + " is stored", "contentUrl": t.containerURL() + "/" + p.ID},
		{"@id": licenseURL, "@type": "CreativeWork", "name": "In Copyright",
			"description": "This item is protected by copyright and/or related rights; uses beyond those the law permits need the rights-holders' permission."},
	}
	// Without a known uploader there is no creator, which bronze refuses.
	uploader := strings.TrimSpace(t.User)
	if uploader != "" {
		root["creator"] = ref("#uploader")
		graph = append(graph, entity{"@id": "#uploader", "@type": "Person", "name": uploader})
	}
	graph = append(graph, props...)
	if p.Provenance.DerivedFrom != "" {
		root["isBasedOn"] = ref(parentID(p.Provenance.DerivedFrom))
		root["mentions"] = ref("#run")
		graph = append(graph, p.run(uploader != "")...)
	}
	if conformsTo := p.conformsTo(); len(conformsTo) > 0 {
		profiles := make([]entity, len(conformsTo))
		for i, uri := range conformsTo {
			profiles[i] = ref(uri)
			graph = append(graph, p.Profile.crateEntity(uri))
		}
		root["conformsTo"] = refs(profiles)
	}
	for _, f := range p.Files {
		graph = append(graph, fileEntity(f))
	}
	return json.MarshalIndent(map[string]any{
		"@context": roCrateContext,
		"@graph":   graph,
	}, "", "    ")
}

// lbf's own properties, then the publisher's by name.
func (p publication) properties(t target) []entity {
	pv := func(name, value string) entity {
		return entity{"@id": "#" + fileID(name), "@type": "PropertyValue", "name": name, "value": value}
	}
	props := []entity{
		pv("subscription_name", orUnknown(t.SubscriptionName)),
		pv("subscription_id", orUnknown(t.SubscriptionID)),
		pv("source_path", filepath.ToSlash(p.Source)),
	}
	for _, k := range slices.Sorted(maps.Keys(p.Provenance.Properties)) {
		props = append(props, pv(k, p.Provenance.Properties[k]))
	}
	return props
}

func parentID(id string) string { return "#source-" + id }

// The parent, the instruments and the run that made the dataset from one with them, as its publisher stated.
func (p publication) run(byUploader bool) []entity {
	prov := p.Provenance
	parent := parentID(prov.DerivedFrom)
	graph := []entity{{"@id": parent, "@type": "Dataset", "identifier": prov.DerivedFrom, "name": prov.DerivedFrom}}
	var tools []entity
	seen := map[string]bool{}
	for _, in := range prov.Instruments {
		tools = append(tools, ref(in.URL))
		if !seen[in.URL] {
			seen[in.URL] = true
			graph = append(graph, entity{"@id": in.URL, "@type": "SoftwareApplication", "name": in.Name, "version": in.Version, "url": in.URL})
		}
	}
	first := prov.Instruments[0]
	run := entity{
		"@id":         "#run",
		"@type":       "CreateAction",
		"name":        fmt.Sprintf("Dataset %s produced by %s", p.ID, first.Name),
		"description": fmt.Sprintf("%s %s made dataset %s from %s, as its publisher stated to lbf", first.Name, first.Version, p.ID, prov.DerivedFrom),
		"instrument":  refs(tools),
		"object":      ref(parent),
		"result":      ref("./"),
	}
	if byUploader {
		run["agent"] = ref("#uploader")
	}
	return append(graph, run)
}

func fileEntity(f localFile) entity {
	file := entity{"@id": fileID(f.Rel), "@type": "File", "name": path.Base(f.Rel), "contentSize": fmt.Sprint(f.Size)}
	if format := mediaTypes[strings.ToLower(path.Ext(f.Rel))]; format != "" {
		file["encodingFormat"] = format
	}
	if f.SHA256 != "" {
		file["sha256"] = f.SHA256
	}
	return file
}

// References to each entity, by its @id.
func refsTo(entities []entity) []entity {
	out := make([]entity, len(entities))
	for i, e := range entities {
		out[i] = ref(e["@id"].(string))
	}
	return out
}

// The profiles a crate declares: Process Run Crate when lbf records how it was made, then bronze up to the chosen one.
func (p publication) conformsTo() []string {
	if p.Profile == nil {
		return nil
	}
	ids := p.Profile.ids()
	if p.Provenance.DerivedFrom != "" {
		ids = append([]string{processRunCrate}, ids...)
	}
	return ids
}

// A profile's entity, named by its title and versioned by the end of its $id.
func (p *profile) crateEntity(uri string) entity {
	e := entity{"@id": uri, "@type": []string{"CreativeWork", "Profile"}, "name": cmp.Or(p.titles()[uri], uri)}
	if v := profileVersion.FindString(uri); v != "" {
		e["version"] = strings.TrimPrefix(v, "/")
	}
	return e
}

type crateFile struct {
	Rel    string
	Size   int64
	SHA256 string
}

// A crate as stored. The crate, written last, is the record of what landed; fetch trusts it over the listing.
type storedCrate struct {
	raw    []byte
	Files  []crateFile
	stated map[string]any
}

func readCrate(raw []byte) (storedCrate, error) {
	var doc struct {
		Context any              `json:"@context"`
		Graph   []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return storedCrate{}, fmt.Errorf("its %s is not valid JSON: %w", crateName, err)
	}
	entities := map[string]map[string]any{}
	for _, e := range doc.Graph {
		if id, ok := e["@id"].(string); ok {
			entities[id] = e
		}
	}
	c := storedCrate{raw: raw}
	files := map[string]bool{}
	for _, part := range asList(entities["./"]["hasPart"]) {
		id := refID(part)
		files[id] = entities[id]["@type"] == "File"
		rel, err := url.PathUnescape(id)
		if err != nil || rel == "" {
			return storedCrate{}, fmt.Errorf("its %s lists an unreadable file %q", crateName, id)
		}
		size, err := strconv.ParseInt(fmt.Sprint(entities[id]["contentSize"]), 10, 64)
		if err != nil {
			return storedCrate{}, fmt.Errorf("its %s gives no size for %s", crateName, rel)
		}
		sum, _ := entities[id]["sha256"].(string)
		c.Files = append(c.Files, crateFile{Rel: rel, Size: size, SHA256: sum})
	}
	if len(c.Files) == 0 {
		return storedCrate{}, fmt.Errorf("its %s lists no files", crateName)
	}
	// Framing time grows with the square of the entities, so what the crate states is read without its files, which refer to nothing.
	delete(entities["./"], "hasPart")
	var graph []any
	for _, e := range doc.Graph {
		if !files[refID(e)] {
			graph = append(graph, e)
		}
	}
	c.stated = map[string]any{"@context": doc.Context, "@graph": graph}
	return c, nil
}

func (c storedCrate) byPath() map[string]crateFile {
	files := make(map[string]crateFile, len(c.Files))
	for _, f := range c.Files {
		files[f.Rel] = f
	}
	return files
}

// Why what is stored is not what the crate records; sums also compares sha256s, which files stored before lbf recorded them lack.
func (c storedCrate) differences(stored map[string]crateFile, sums bool) []string {
	var problems []string
	listed := map[string]bool{crateName: true}
	for _, f := range c.Files {
		listed[f.Rel] = true
		s, ok := stored[f.Rel]
		switch {
		case !ok:
			problems = append(problems, f.Rel+": listed in the crate but not stored")
		case s.Size != f.Size:
			problems = append(problems, fmt.Sprintf("%s: stored as %d bytes, but the crate records %d", f.Rel, s.Size, f.Size))
		case sums && s.SHA256 != f.SHA256:
			problems = append(problems, fmt.Sprintf("%s: stored with sha256 %q, but the crate records %q", f.Rel, s.SHA256, f.SHA256))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(stored)) {
		if !listed[name] {
			problems = append(problems, name+": stored but not listed in the crate")
		}
	}
	return problems
}

// How what the crate states differs from what this publication would; a name or description lbf filled in follows the folder, so it only has to match when given.
func (c storedCrate) differsFrom(p publication) ([]string, error) {
	stated, conformsTo, err := c.statement()
	if err != nil {
		return nil, err
	}
	if p.Provenance.Name == "" {
		stated.Name = ""
	}
	if p.Provenance.Description == "" {
		stated.Description = ""
	}
	var problems []string
	if !stated.sameAs(p.Provenance) {
		problems = append(problems, "its name, description, parent, instruments or properties differ from these")
	}
	if !slices.Equal(slices.Sorted(slices.Values(conformsTo)), slices.Sorted(slices.Values(p.conformsTo()))) {
		problems = append(problems, "it was checked against other profiles")
	}
	return problems, nil
}

// How lbf reads a crate back, whichever version wrote it: earlier ones record the parent as wasDerivedFrom and do not mention the run.
var statementFrame = map[string]any{
	"@context": []any{roCrateContext, map[string]any{
		"additionalProperty": map[string]any{"@id": "http://schema.org/additionalProperty", "@container": "@index", "@index": "name"},
		"isBasedOn":          map[string]any{"@id": "http://schema.org/isBasedOn", "@container": "@set"},
		"wasDerivedFrom":     map[string]any{"@id": "http://www.w3.org/ns/prov#wasDerivedFrom", "@container": "@set"},
		"instrument":         map[string]any{"@id": "http://schema.org/instrument", "@container": "@set"},
		"conformsTo":         map[string]any{"@id": "http://purl.org/dc/terms/conformsTo", "@container": "@set"},
	}},
	"@id":      "./",
	"@reverse": map[string]any{"result": map[string]any{}},
}

// What the crate states about its dataset, leaving out lbf's own properties, and the profiles it declares.
func (c storedCrate) statement() (provenance, []string, error) {
	view, err := frameCrate(c.stated, statementFrame)
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
	for _, parent := range slices.Concat(asList(view["isBasedOn"]), asList(view["wasDerivedFrom"])) {
		p.DerivedFrom = str(parent, "identifier")
	}
	for _, run := range asList(obj(view["@reverse"])["result"]) {
		for _, in := range asList(obj(run)["instrument"]) {
			p.Instruments = append(p.Instruments, instrument{str(in, "name"), str(in, "version"), str(in, "url")})
		}
	}
	// What lbf wrote when the publisher gave nothing, this version or an earlier one, is not what they stated.
	id, source := str(view, "identifier"), str(obj(view["additionalProperty"])["source_path"], "value")
	if p.Name == id || p.Name == defaultName(source) {
		p.Name = ""
	}
	if slices.Contains([]string{"Dataset " + id, "Bronze-layer dataset " + id, defaultDescription(source, len(c.Files), p.DerivedFrom)}, p.Description) {
		p.Description = ""
	}
	var conformsTo []string
	for _, prof := range asList(view["conformsTo"]) {
		conformsTo = append(conformsTo, str(prof, "@id"))
	}
	return p, conformsTo, nil
}

// Percent-encodes like Python's urllib.parse.quote, as ro-crate-py does.
func fileID(rel string) string {
	var b strings.Builder
	for _, c := range []byte(rel) {
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("_.-~/", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
