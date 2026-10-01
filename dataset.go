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
func buildCrate(id, source string, files []localFile, t target, prov provenance, profileID string, published time.Time) ([]byte, error) {
	stamp := published.UTC().Format("2006-01-02T15:04:05Z")

	parts := make([]entity, len(files))
	for i, f := range files {
		parts[i] = ref(f.Rel)
	}

	props := []struct{ name, value string }{
		{"subscription_name", orUnknown(t.SubscriptionName)},
		{"subscription_id", orUnknown(t.SubscriptionID)},
		{"source_path", filepath.ToSlash(source)},
	}
	names := make([]string, 0, len(prov.Properties))
	for k := range prov.Properties {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
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

	var profiles []string
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
		profiles = append(profiles, processRunCrate)
	}
	if profileID != "" {
		profiles = append(profiles, profileID)
	}
	if len(profiles) > 0 {
		refs := make([]entity, len(profiles))
		for i, uri := range profiles {
			refs[i] = ref(uri)
			graph = append(graph, entity{"@id": uri, "@type": []string{"CreativeWork", "Profile"}, "name": uri})
		}
		root["conformsTo"] = refs
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
