package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
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
	if conformsTo := p.Profile.conformsTo(p.Provenance.DerivedFrom != ""); len(conformsTo) > 0 {
		profiles := make([]entity, len(conformsTo))
		for i, uri := range conformsTo {
			profiles[i] = ref(uri)
			graph = append(graph, p.Profile.crateEntities(uri)...)
		}
		root["conformsTo"] = refs(profiles)
		graph = append(graph,
			entity{"@id": roleSchema, "@type": "DefinedTerm", "name": "Schema", "description": "A schema the profile's data must meet"},
			entity{"@id": roleMapping, "@type": "DefinedTerm", "name": "Mapping", "description": "How the crate is laid out for the profile's schema to check it"})
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

// A file a profile's entity carries, in the role it plays for the profile.
type resource struct {
	role, suffix, format string
	text                 []byte
}

// A profile's entity, which records what it builds on and carries the exact schema the dataset was checked against.
func (p *profile) crateEntities(uri string) []entity {
	e := entity{"@id": uri, "@type": []string{"CreativeWork", "Profile"}, "name": uri}
	at, atErr := publishedAs(uri)
	if atErr == nil {
		e["version"] = at.version
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
	if atErr == nil {
		slug = "#profile-" + fileID(at.name+"-"+at.version)
	}
	resources := []resource{{roleSchema, "schema", "application/schema+json", r.raw}}
	if i == 0 {
		resources = append(resources, resource{roleMapping, "frame", "application/ld+json", bronzeFrameJSON})
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
