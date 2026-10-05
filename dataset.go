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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	petname "github.com/dustinkirkland/golang-petname"
)

const licenseURL = "https://rightsstatements.org/vocab/InC/1.0/"

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

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
// Symlinks are followed, as cp -RL does.
func datasetFiles(input string) (string, []localFile, error) {
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", nil, err
	}
	source, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", nil, fmt.Errorf("input %s: %w", input, err)
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", nil, err
	}
	base := filepath.Base(source)

	var files []localFile
	var problems []string
	if info.IsDir() {
		walkDataset(source, base, nil, &files, &problems)
	} else {
		files = []localFile{{Path: source, Rel: base, Size: info.Size(), ModTime: info.ModTime()}}
	}
	slices.SortFunc(files, func(a, b localFile) int { return strings.Compare(a.Rel, b.Rel) })
	problems = append(problems, checkFiles(files)...)
	if len(problems) > 0 {
		return "", nil, problemList(fmt.Sprintf("%s cannot be published as it is, so nothing was uploaded:", input), problems)
	}
	return source, files, nil
}

func walkDataset(dir, rel string, ancestors []string, files *[]localFile, problems *[]string) {
	resolved, err := filepath.EvalSymlinks(dir)
	if err == nil && slices.Contains(ancestors, resolved) {
		*problems = append(*problems, rel+": symlink loop back to a folder above it")
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s: cannot be read (%v)", rel, cause(err)))
		return
	}
	ancestors = append(ancestors, resolved)
	for _, e := range entries {
		if excluded(e.Name()) {
			continue
		}
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
		case info.Mode().IsRegular():
			*files = append(*files, localFile{Path: path, Rel: r, Size: info.Size(), ModTime: info.ModTime()})
		default:
			*problems = append(*problems, r+": not a regular file")
		}
	}
}

var windowsReserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$`)

// Datasets never change once landed, so a name that cannot be fetched everywhere is refused up front.
func checkFiles(files []localFile) []string {
	var problems []string
	seen := map[string]string{}
	for _, f := range files {
		if p := nameProblem(f.Rel); p != "" {
			problems = append(problems, f.Rel+": "+p)
		}
		if f.Rel == crateName {
			problems = append(problems, f.Rel+": would be replaced by the crate lbf writes; rename it or publish its folder")
		}
		folded := strings.ToLower(f.Rel)
		if other, ok := seen[folded]; ok {
			problems = append(problems, fmt.Sprintf("%s and %s differ only in case, so one would overwrite the other on macOS and Windows", other, f.Rel))
		}
		seen[folded] = f.Rel
		fh, err := os.Open(f.Path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: cannot be read (%v)", f.Rel, cause(err)))
			continue
		}
		fh.Close()
	}
	return problems
}

func nameProblem(rel string) string {
	for seg := range strings.SplitSeq(rel, "/") {
		switch {
		case strings.Contains(seg, `\`):
			return `contains \, which Azure turns into a folder separator`
		case strings.ContainsAny(seg, `:*?"<>|`):
			return `contains one of : * ? " < > |, which Windows cannot store`
		case strings.ContainsFunc(seg, unicode.IsControl):
			return "contains a control character"
		case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
			return fmt.Sprintf("%q ends in a dot or space, which Windows drops", seg)
		case windowsReserved.MatchString(seg):
			return fmt.Sprintf("%q is a reserved name on Windows", seg)
		}
	}
	return ""
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

// Same graph as rocrate_generator.py; encoding/json sorts keys, matching its output.
func buildCrate(id, source string, files []localFile, t target, prov provenance, conformsTo []string, published time.Time) ([]byte, error) {
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
		propRefs[i] = ref("#" + p.name)
	}

	description := "Dataset " + id
	if t.Container == "bronze" {
		description = "Bronze-layer dataset " + id
	}
	root := entity{
		"@id":                "./",
		"@type":              "Dataset",
		"identifier":         id,
		"name":               id,
		"description":        description,
		"datePublished":      stamp,
		"license":            licenseURL,
		"creator":            ref("#uploader"),
		"distribution":       ref("#blob-location"),
		"additionalProperty": propRefs,
		"hasPart":            parts,
	}
	graph := []entity{
		root,
		{
			"@id":        "ro-crate-metadata.json",
			"@type":      "CreativeWork",
			"about":      ref("./"),
			"conformsTo": ref("https://w3id.org/ro/crate/1.2"),
		},
		{"@id": "#uploader", "@type": "Person", "name": orUnknown(t.User)},
		{"@id": "#blob-location", "@type": "DataDownload", "contentUrl": t.containerURL() + "/" + id},
	}
	for _, p := range props {
		graph = append(graph, entity{"@id": "#" + p.name, "@type": "PropertyValue", "name": p.name, "value": p.value})
	}

	if prov.DerivedFrom != "" {
		sourceID := "#source-" + prov.DerivedFrom
		root["wasDerivedFrom"] = ref(sourceID)
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
		var instrumentRef any = tools
		if len(tools) == 1 {
			instrumentRef = tools[0]
		}
		graph = append(graph, entity{
			"@id":        "#run",
			"@type":      "CreateAction",
			"name":       fmt.Sprintf("Dataset %s produced by %s", id, prov.Instruments[0].Name),
			"endTime":    stamp,
			"instrument": instrumentRef,
			"object":     ref(sourceID),
			"result":     ref("./"),
			"agent":      ref("#uploader"),
		})
	}
	if len(conformsTo) > 0 {
		refs := make([]entity, len(conformsTo))
		for i, uri := range conformsTo {
			refs[i] = ref(uri)
			graph = append(graph, entity{"@id": uri, "@type": []string{"CreativeWork", "Profile"}, "name": uri})
		}
		root["conformsTo"] = refs
	}

	for _, f := range files {
		file := entity{"@id": fileID(f.Rel), "@type": "File", "contentSize": fmt.Sprint(f.Size)}
		if f.SHA256 != "" {
			file["sha256"] = f.SHA256
		}
		graph = append(graph, file)
	}

	return json.MarshalIndent(map[string]any{
		"@context": "https://w3id.org/ro/crate/1.2/context",
		"@graph":   graph,
	}, "", "    ")
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
