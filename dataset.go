package main

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	petname "github.com/dustinkirkland/golang-petname"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const licenseURL = "https://rightsstatements.org/vocab/InC/1.0/"

// A fixed table, not the OS's, so a crate is the same wherever it is written.
var mediaTypes = map[string]string{
	".csv": "text/csv", ".tsv": "text/tab-separated-values", ".txt": "text/plain", ".md": "text/markdown",
	".json": "application/json", ".xml": "application/xml", ".pdf": "application/pdf",
	".tif": "image/tiff", ".tiff": "image/tiff", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".zip": "application/zip", ".gz": "application/gzip", ".parquet": "application/vnd.apache.parquet",
	".h5": "application/x-hdf5", ".hdf5": "application/x-hdf5",
}

var (
	validID  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	mintedID = regexp.MustCompile(`^[0-9]{8}-[a-z]+-[a-z]+-[0-9a-f]{4}$`)
)

type localFile struct {
	Path    string
	Rel     string // slash-separated, relative to the upload root: <input name>/...
	Size    int64
	ModTime time.Time
	SHA256  string // set once uploaded
}

func excluded(name string) bool {
	return name == ".DS_Store" || strings.HasPrefix(name, "._")
}

// Mirrors azcopy's layout: a directory lands as <id>/<dirname>/..., a file as <id>/<filename>.
// Symlinks are followed, as cp -RL does, but the dataset takes the name it was given by.
func datasetFiles(input string) (string, []localFile, error) {
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", nil, fmt.Errorf("input %s: %w", input, err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", nil, fmt.Errorf("input %s: %w", input, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	base := filepath.Base(abs)
	source := filepath.Join(parent, base)

	var files []localFile
	var problems []string
	switch {
	case info.IsDir():
		walkDataset(resolved, base, nil, &files, &problems)
	case info.Mode().IsRegular():
		files = []localFile{{Path: resolved, Rel: base, Size: info.Size(), ModTime: info.ModTime()}}
	default:
		problems = append(problems, base+": not a regular file")
	}
	slices.SortFunc(files, func(a, b localFile) int { return strings.Compare(a.Rel, b.Rel) })
	problems = append(problems, checkFiles(files)...)
	if len(problems) > 0 {
		return "", nil, problemList(fmt.Sprintf("%s cannot be published as it is, so nothing was uploaded:", input), problems)
	}
	return source, files, nil
}

// EvalSymlinks does not follow Windows junctions, so loops are found by file identity.
func walkDataset(dir, rel string, ancestors []os.FileInfo, files *[]localFile, problems *[]string) {
	self, err := os.Stat(dir)
	if err == nil && slices.ContainsFunc(ancestors, func(a os.FileInfo) bool { return os.SameFile(a, self) }) {
		*problems = append(*problems, rel+": symlink loop back to a folder above it")
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s: cannot be read (%v)", rel, cause(err)))
		return
	}
	ancestors = append(ancestors, self)
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		r := rel + "/" + e.Name()
		info, err := os.Stat(path)
		switch {
		case err != nil && e.Type()&fs.ModeSymlink != 0:
			dest, _ := os.Readlink(path)
			*problems = append(*problems, fmt.Sprintf("%s: broken symlink to %s", r, dest))
		case err != nil:
			*problems = append(*problems, fmt.Sprintf("%s: %v", r, cause(err)))
		case info.IsDir():
			walkDataset(path, r, ancestors, files, problems)
		case excluded(e.Name()):
		case info.Mode().IsRegular():
			*files = append(*files, localFile{Path: path, Rel: r, Size: info.Size(), ModTime: info.ModTime()})
		default:
			*problems = append(*problems, r+": not a regular file")
		}
	}
}

var windowsReserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³]|conin\$|conout\$)$`)

// Datasets never change once landed, so a name that cannot be fetched everywhere is refused up front.
func checkFiles(files []localFile) []string {
	var problems []string
	if len(files) > 0 && clashesWithCrate(files[0].Rel, true) {
		top, _, _ := strings.Cut(files[0].Rel, "/")
		problems = append(problems, top+": would be replaced by the crate lbf writes; rename it or publish its folder")
	}
	rels := make([]string, len(files))
	for i, f := range files {
		rels[i] = f.Rel
		if p := cmp.Or(encodingProblem(f.Rel), nameProblem(f.Rel)); p != "" {
			problems = append(problems, f.Rel+": "+p)
		}
		fh, err := os.Open(f.Path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: cannot be read (%v)", f.Rel, cause(err)))
			continue
		}
		fh.Close()
	}
	return append(problems, sameNameProblems(rels, "macOS and Windows")...)
}

// Azure's limits on a blob name, which is <id>/<rel>.
func blobNameProblems(id string, files []localFile) []string {
	var problems []string
	for _, f := range files {
		name := id + "/" + f.Rel
		switch {
		case len(name) > 1024:
			problems = append(problems, fmt.Sprintf("%s: too long for Azure, which allows 1024 characters including the dataset ID (this is %d)", f.Rel, len(name)))
		case strings.Count(name, "/")+1 > 254:
			problems = append(problems, fmt.Sprintf("%s: nested too deeply for Azure, which allows 254 path segments including the dataset ID", f.Rel))
		}
	}
	return problems
}

// Linux allows 255 bytes per name, and Azure blob names must be UTF-8.
func encodingProblem(rel string) string {
	if !utf8.ValidString(rel) {
		return "is not valid UTF-8, which Azure blob names must be"
	}
	for seg := range strings.SplitSeq(rel, "/") {
		if len(seg) > 255 {
			return fmt.Sprintf("%q is %d bytes long, but Linux allows at most 255 per name", seg, len(seg))
		}
	}
	return ""
}

func nameProblem(rel string) string {
	for seg := range strings.SplitSeq(rel, "/") {
		stem, _, _ := strings.Cut(seg, ".")
		switch {
		case strings.Contains(seg, `\`):
			return `contains \, which Azure turns into a folder separator`
		case strings.ContainsAny(seg, `:*?"<>|`):
			return `contains one of : * ? " < > |, which Windows cannot store`
		case strings.ContainsFunc(seg, unicode.IsControl):
			return "contains a control character"
		case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
			return fmt.Sprintf("%q ends in a dot or space, which Windows drops", seg)
		case windowsReserved.MatchString(strings.TrimRight(stem, " ")):
			return fmt.Sprintf("%q is a reserved name on Windows", seg)
		}
	}
	return ""
}

// The name macOS and Windows store a path under: one Unicode form, and case folded.
func nameKey(rel string) string {
	return cases.Fold().String(norm.NFC.String(rel))
}

func clashesWithCrate(rel string, fold bool) bool {
	top, _, _ := strings.Cut(rel, "/")
	return top == crateName || fold && nameKey(top) == nameKey(crateName)
}

// Every file and folder must keep its own name where names are case and normalisation insensitive.
func sameNameProblems(rels []string, where string) []string {
	var problems []string
	seen := map[string]string{}
	reported := map[string]bool{}
	for _, rel := range rels {
		for i := 0; i <= len(rel); i++ {
			if i < len(rel) && rel[i] != '/' {
				continue
			}
			path := rel[:i]
			key := nameKey(path)
			other, ok := seen[key]
			if !ok {
				seen[key] = path
				continue
			}
			if other == path {
				continue
			}
			how := "differ only in case"
			if norm.NFC.String(other) == norm.NFC.String(path) {
				how = "are the same name in different Unicode forms"
			}
			if p := fmt.Sprintf("%s and %s %s, so %s cannot hold both", other, path, how, where); !reported[p] {
				reported[p] = true
				problems = append(problems, p)
			}
			break
		}
	}
	return problems
}

func problemList(heading string, problems []string) error {
	const shown = 10
	var b strings.Builder
	b.WriteString(heading)
	for _, p := range problems[:min(len(problems), shown)] {
		b.WriteString("\n  " + p)
	}
	if len(problems) > shown {
		fmt.Fprintf(&b, "\n  ...and %d more", len(problems)-shown)
	}
	return errors.New(b.String())
}

func cause(err error) error {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err
	}
	return err
}

func newID() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s-%s", time.Now().Format("20060102"), petname.Generate(2, "-"), hex.EncodeToString(b))
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

var profileVersion = regexp.MustCompile(`/v?[0-9]+(\.[0-9]+)*$`)

const (
	roCrateSpec = "https://w3id.org/ro/crate/1.3"
	roleSchema  = "http://www.w3.org/ns/dx/prof/role/schema"
	roleMapping = "http://www.w3.org/ns/dx/prof/role/mapping"
)

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

// RO-Crate 1.3, flattened; encoding/json sorts keys.
func buildCrate(id, source string, files []localFile, t target, prov provenance, prof *profile, published time.Time) ([]byte, error) {
	stamp := timestamp(published)

	parts := make([]entity, len(files))
	for i, f := range files {
		parts[i] = ref(fileID(f.Rel))
	}

	props := []struct{ name, value string }{
		{"subscription_name", orUnknown(t.SubscriptionName)},
		{"subscription_id", orUnknown(t.SubscriptionID)},
		{"source_path", filepath.ToSlash(source)},
	}
	for _, k := range slices.Sorted(maps.Keys(prov.Properties)) {
		props = append(props, struct{ name, value string }{k, prov.Properties[k]})
	}
	propRefs := make([]entity, len(props))
	for i, p := range props {
		propRefs[i] = ref("#" + fileID(p.name))
	}

	root := entity{
		"@id":                "./",
		"@type":              "Dataset",
		"identifier":         id,
		"name":               cmp.Or(prov.Name, defaultName(source)),
		"description":        cmp.Or(prov.Description, defaultDescription(source, len(files), prov.DerivedFrom)),
		"datePublished":      stamp,
		"license":            ref(licenseURL),
		"distribution":       ref("#blob-location"),
		"additionalProperty": refs(propRefs),
		"hasPart":            refs(parts),
	}
	graph := []entity{
		root,
		{
			"@id":        crateName,
			"@type":      "CreativeWork",
			"about":      ref("./"),
			"conformsTo": ref(roCrateSpec),
		},
		{"@id": "#blob-location", "@type": "DataDownload", "name": "Where " + id + " is stored", "contentUrl": t.containerURL() + "/" + id},
		{"@id": licenseURL, "@type": "CreativeWork", "name": "In Copyright",
			"description": "This item is protected by copyright and/or related rights; uses beyond those the law permits need the rights-holders' permission."},
	}
	// Without a known uploader there is no creator, which bronze refuses.
	uploader := strings.TrimSpace(t.User)
	if uploader != "" {
		root["creator"] = ref("#uploader")
		graph = append(graph, entity{"@id": "#uploader", "@type": "Person", "name": uploader})
	}
	for _, p := range props {
		graph = append(graph, entity{"@id": "#" + fileID(p.name), "@type": "PropertyValue", "name": p.name, "value": p.value})
	}

	if prov.DerivedFrom != "" {
		sourceID := "#source-" + prov.DerivedFrom
		root["isBasedOn"] = ref(sourceID)
		root["mentions"] = ref("#run")
		graph = append(graph, entity{"@id": sourceID, "@type": "Dataset", "identifier": prov.DerivedFrom, "name": prov.DerivedFrom})

		var tools []entity
		seen := map[string]bool{}
		for _, in := range prov.Instruments {
			tools = append(tools, ref(in.URL))
			if !seen[in.URL] {
				seen[in.URL] = true
				graph = append(graph, entity{"@id": in.URL, "@type": "SoftwareApplication", "name": in.Name, "version": in.Version, "url": in.URL})
			}
		}
		run := entity{
			"@id":   "#run",
			"@type": "CreateAction",
			"name":  fmt.Sprintf("Dataset %s produced by %s", id, prov.Instruments[0].Name),
			"description": fmt.Sprintf("%s %s made dataset %s from %s, as its publisher stated to lbf",
				prov.Instruments[0].Name, prov.Instruments[0].Version, id, prov.DerivedFrom),
			"instrument": refs(tools),
			"object":     ref(sourceID),
			"result":     ref("./"),
		}
		if uploader != "" {
			run["agent"] = ref("#uploader")
		}
		graph = append(graph, run)
	}
	if conformsTo := prof.conformsTo(prov.DerivedFrom != ""); len(conformsTo) > 0 {
		ids := make([]entity, len(conformsTo))
		for i, uri := range conformsTo {
			ids[i] = ref(uri)
			graph = append(graph, prof.entities(uri)...)
		}
		root["conformsTo"] = refs(ids)
		graph = append(graph,
			entity{"@id": roleSchema, "@type": "DefinedTerm", "name": "Schema", "description": "A schema the profile's data must meet"},
			entity{"@id": roleMapping, "@type": "DefinedTerm", "name": "Mapping", "description": "How the crate is laid out for the profile's schema to check it"})
	}

	for _, f := range files {
		file := entity{"@id": fileID(f.Rel), "@type": "File", "name": path.Base(f.Rel), "contentSize": fmt.Sprint(f.Size)}
		if format := mediaTypes[strings.ToLower(path.Ext(f.Rel))]; format != "" {
			file["encodingFormat"] = format
		}
		if f.SHA256 != "" {
			file["sha256"] = f.SHA256
		}
		graph = append(graph, file)
	}

	return json.MarshalIndent(map[string]any{
		"@context": roCrateContext,
		"@graph":   graph,
	}, "", "    ")
}

// The profiles a crate declares: Process Run Crate when lbf records how it was made, then bronze up to the chosen one.
func (p *profile) conformsTo(derived bool) []string {
	if p == nil {
		return nil
	}
	ids := p.ids()
	if derived {
		ids = append([]string{processRunCrate}, ids...)
	}
	return ids
}

// A profile's entity, which records what it builds on and carries the exact schema the dataset was checked against.
func (p *profile) entities(uri string) []entity {
	e := entity{"@id": uri, "@type": []string{"CreativeWork", "Profile"}, "name": uri}
	if v := profileVersion.FindString(uri); v != "" {
		e["version"] = strings.TrimPrefix(v, "/")
	}
	if uri == processRunCrate {
		e["name"] = "Process Run Crate"
		return []entity{e}
	}
	i := slices.IndexFunc(p.rules, func(r rule) bool { return r.id == uri })
	r := p.rules[i]
	e["name"] = cmp.Or(r.title, uri)
	if r.parent != "" {
		e["isProfileOf"] = ref(r.parent)
	}
	slug := fmt.Sprintf("#profile-%d", i)
	if u, err := url.Parse(uri); err == nil {
		if segs := strings.Split(strings.Trim(u.Path, "/"), "/"); len(segs) >= 2 {
			slug = "#profile-" + fileID(strings.Join(segs[len(segs)-2:], "-"))
		}
	}
	resources := []struct {
		role, suffix, format string
		text                 []byte
	}{{roleSchema, "schema", "application/schema+json", r.raw}}
	if i == 0 {
		resources = append(resources, struct {
			role, suffix, format string
			text                 []byte
		}{roleMapping, "frame", "application/ld+json", bronzeFrameJSON})
	}
	out := []entity{e}
	var ids []entity
	for _, res := range resources {
		descriptor := slug + "-" + res.suffix
		name := fmt.Sprintf("The %s of %s", res.suffix, e["name"])
		ids = append(ids, ref(descriptor))
		artifact := entity{"@id": descriptor + ".json", "@type": "CreativeWork", "name": name, "encodingFormat": res.format, "text": string(res.text)}
		if res.role == roleSchema {
			artifact["conformsTo"] = ref(jsonSchemaDialect(res.text))
		}
		out = append(out,
			entity{"@id": descriptor, "@type": []string{"CreativeWork", "ResourceDescriptor"}, "name": name, "hasRole": ref(res.role), "hasArtifact": ref(descriptor + ".json")},
			artifact)
	}
	e["hasResource"] = refs(ids)
	return out
}

func jsonSchemaDialect(raw []byte) string {
	var head struct {
		Schema string `json:"$schema"`
	}
	_ = json.Unmarshal(raw, &head)
	return cmp.Or(head.Schema, "https://json-schema.org/draft/2020-12/schema")
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

func orUnknown(s string) string {
	return cmp.Or(strings.TrimSpace(s), "unknown")
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
