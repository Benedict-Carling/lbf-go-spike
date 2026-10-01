package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	petname "github.com/dustinkirkland/golang-petname"
)

const licenseURL = "https://rightsstatements.org/vocab/InC/1.0/"

type localFile struct {
	Path string
	Rel  string // slash-separated, relative to the upload root: <input name>/...
	Size int64
}

func excluded(name string) bool {
	return name == ".DS_Store" || strings.HasPrefix(name, "._")
}

// Mirrors azcopy's layout: a directory lands as <id>/<dirname>/..., a file as <id>/<filename>.
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

	if !info.IsDir() {
		return source, []localFile{{source, base, info.Size()}}, nil
	}

	var files []localFile
	err = filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || excluded(d.Name()) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		files = append(files, localFile{path, base + "/" + filepath.ToSlash(rel), fi.Size()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return source, files, err
}

func newID() string {
	petname.NonDeterministicMode()
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s-%s", time.Now().Format("20060102"), petname.Generate(2, "-"), hex.EncodeToString(b))
}

type entity map[string]any

func ref(id string) entity { return entity{"@id": id} }

// Same graph as rocrate_generator.py; encoding/json sorts keys, matching its output.
func buildCrate(id, source string, files []localFile, t target, published time.Time) ([]byte, error) {
	parts := make([]entity, len(files))
	for i, f := range files {
		parts[i] = ref(f.Rel)
	}

	props := []struct{ name, value string }{
		{"subscription_name", orUnknown(t.SubscriptionName)},
		{"subscription_id", orUnknown(t.SubscriptionID)},
		{"source_path", filepath.ToSlash(source)},
	}
	propRefs := make([]entity, len(props))
	for i, p := range props {
		propRefs[i] = ref("#" + p.name)
	}

	graph := []entity{
		{
			"@id":                "./",
			"@type":              "Dataset",
			"identifier":         id,
			"name":               id,
			"description":        "Bronze-layer dataset " + id,
			"datePublished":      published.UTC().Format("2006-01-02T15:04:05Z"),
			"license":            licenseURL,
			"creator":            ref("#uploader"),
			"distribution":       ref("#blob-location"),
			"additionalProperty": propRefs,
			"hasPart":            parts,
		},
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
	for _, f := range files {
		graph = append(graph, entity{"@id": f.Rel, "@type": "File", "contentSize": fmt.Sprint(f.Size)})
	}

	return json.MarshalIndent(map[string]any{
		"@context": "https://w3id.org/ro/crate/1.2/context",
		"@graph":   graph,
	}, "", "    ")
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return strings.TrimSpace(s)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
