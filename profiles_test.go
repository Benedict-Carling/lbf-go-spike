package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piprate/json-gold/ld"
)

func TestPropertyFlagsAddToTheProvenance(t *testing.T) {
	dir := t.TempDir()
	common := filepath.Join(dir, "common.json")
	writeFile(t, common, `{"name": "Plate P-0001", "properties": {"operator": "A. Researcher"}}`)

	p, err := provenanceFlags{file: common, props: []string{"plate_id=P-0001", "note=a=b"}, description: "Read at 450 nm"}.load()
	must(t, err)
	if p.Name != "Plate P-0001" || p.Description != "Read at 450 nm" || p.Properties["operator"] != "A. Researcher" ||
		p.Properties["plate_id"] != "P-0001" || p.Properties["note"] != "a=b" {
		t.Fatalf("got %+v", p)
	}

	for name, f := range map[string]provenanceFlags{
		"given twice":           {file: common, props: []string{"operator=B"}},
		"not NAME=VALUE":        {props: []string{"plate_id"}},
		"no name":               {props: []string{"=P-0001"}},
		"name in file and flag": {file: common, name: "Other"},
		"set by lbf":            {props: []string{"source_path=/x"}},
	} {
		if _, err := f.load(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func derivedPublication(t *testing.T, prof *profile) publication {
	t.Helper()
	return publication{
		ID:     newID(),
		Source: "/data/plate1",
		Files:  []localFile{{Rel: "plate1/readings.csv", Size: 904}},
		Provenance: provenance{
			DerivedFrom: "20261007-causal-bison-9ece",
			Instruments: []instrument{{"plate-summary", "0.1.0", "https://example.org/plate-summary/v0.1.0"}},
			Properties:  map[string]string{"plate_id": "P-0001"},
		},
		Profile: prof,
	}
}

func TestCrateUsesOnlyROCrateTerms(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "minimal-silver", "0.1.0", "profile.json"), minimalSilverProfile)
	writeFile(t, filepath.Join(root, "cell-painting", "0.1.0", "profile.json"), cellPaintingProfile)
	prof, err := loadProfile(filepath.Join(root, "cell-painting", "0.1.0"))
	must(t, err)
	pub := derivedPublication(t, prof)
	crate, err := pub.buildCrate(target{User: "u", Container: "silver"}, time.Now())
	must(t, err)
	if err := checkTerms(crate); err != nil {
		t.Fatalf("crate uses a term outside its context: %v", err)
	}

	var doc map[string]any
	must(t, json.Unmarshal(crate, &doc))
	doc["@graph"].([]any)[0].(map[string]any)["plate_id"] = "P-0001"
	bad, _ := json.Marshal(doc)
	if err := checkTerms(bad); err == nil {
		t.Fatal("a key that is not a term was accepted")
	}
}

func TestCrateCarriesEachProfilesSchema(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "minimal-silver", "0.1.0", "profile.json"), minimalSilverProfile)
	prof, err := loadProfile(filepath.Join(root, "minimal-silver", "0.1.0"))
	must(t, err)
	pub := derivedPublication(t, prof)
	pub.Provenance.Properties["sample_id"] = "S1"
	crate, err := pub.crate(target{User: "u", Container: "silver"})
	must(t, err)

	_, schemas, err := crateStatement(crate)
	must(t, err)
	for _, r := range prof.rules {
		if schemas[r.id] != string(r.raw) {
			t.Errorf("%s: crate carries %q", r.id, schemas[r.id])
		}
	}
	s := string(crate)
	for _, want := range []string{
		`"@context": "https://w3id.org/ro/crate/1.3/context"`, `"@id": "https://w3id.org/ro/crate/1.3"`, processRunCrate,
		`"isBasedOn": {`, `"mentions": {`, `"isProfileOf": {`, roleSchema, roleMapping, `"name": "plate1"`,
		`"description": "Dataset of 1 file from plate1, derived from 20261007-causal-bison-9ece"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("crate lacks %s", want)
		}
	}
	for _, unwanted := range []string{"wasDerivedFrom", "endTime"} {
		if strings.Contains(s, unwanted) {
			t.Errorf("crate still has %s", unwanted)
		}
	}
}

func TestProfileErrorsSayWhichFlagFixesThem(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "minimal-silver", "0.1.0", "profile.json"), minimalSilverProfile)
	writeFile(t, filepath.Join(root, "cell-painting", "0.1.0", "profile.json"), cellPaintingProfile)
	prof, err := loadProfile(filepath.Join(root, "cell-painting", "0.1.0"))
	must(t, err)
	tg := target{User: "u", Container: "silver"}

	pub := derivedPublication(t, prof)
	pub.Provenance.Properties = map[string]string{"sample_id": "S1"}
	_, err = pub.crate(tg)
	if err == nil || !strings.Contains(err.Error(), "missing --property plate_id=...") || !strings.Contains(err.Error(), "Plate barcode; e.g. P001") {
		t.Errorf("missing property: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "files: no file is what the profile asks for") {
		t.Logf("%v", err)
	}

	pub = derivedPublication(t, prof)
	pub.Provenance.DerivedFrom, pub.Provenance.Instruments = "", nil
	pub.Provenance.Properties["sample_id"] = "S1"
	if _, err := pub.crate(tg); err == nil || !strings.Contains(err.Error(), "missing --derived-from ID and --instrument") {
		t.Errorf("no parent: %v", err)
	}
}

func profileJSON(name, version, parent, extra string) string {
	return `{"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "https://w3id.org/lbf/profiles/` + name + `/` + version +
		`", "$ref": "` + parent + `", "title": "` + name + `"` + extra + `}`
}

func TestResumeRefusesAnotherSchemaUnderTheSameID(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	src := t.TempDir()
	dir := filepath.Join(src, "plate-read", "0.1.0")
	writeFile(t, filepath.Join(dir, "profile.json"), profileJSON("plate-read", "0.1.0", bronzeProfileID(t), ""))
	data := dataset(t, map[string]string{"readings.csv": "x"})
	id := newID()

	pub, err := preparePublication(data, provenanceFlags{}, []string{dir}, id)
	must(t, err)
	must(t, upload(ctx, tg, pub))
	must(t, upload(ctx, tg, pub))

	writeFile(t, filepath.Join(dir, "profile.json"), profileJSON("plate-read", "0.1.0", bronzeProfileID(t), `, "description": "edited in place"`))
	pub, err = preparePublication(data, provenanceFlags{}, []string{dir}, id)
	must(t, err)
	err = upload(ctx, tg, pub)
	if _, ok := errors.AsType[idTaken](err); !ok || !strings.Contains(err.Error(), "other versions of their schemas") {
		t.Fatalf("got %v", err)
	}
}

// JSON-LD drops a key that is not a term of the context without a word.
func checkTerms(crate []byte) error {
	opts, err := jsonld()
	if err != nil {
		return err
	}
	doc, err := ld.DocumentFromReader(bytes.NewReader(crate))
	if err != nil {
		return err
	}
	sopts := opts.Copy()
	sopts.SafeMode = true
	_, err = ld.NewJsonLdProcessor().Expand(doc, sopts)
	return err
}

func TestFilesSetAsideFrameAsFramingWould(t *testing.T) {
	prof, err := loadProfile("")
	must(t, err)
	files := []localFile{{Rel: "run1/a b.csv", Size: 3, SHA256: "ab"}, {Rel: "run1/x/y.tif", Size: 5}}
	prov := provenance{DerivedFrom: "20260101-x-y-0000", Instruments: []instrument{{"p", "1", "https://example.org/p"}}, Properties: map[string]string{"plate id": "P1"}}
	crate, err := publication{ID: "20260102-a-b-1234", Source: "/data/run1", Files: files, Provenance: prov, Profile: prof}.buildCrate(target{User: "u"}, time.Now())
	must(t, err)
	fast, err := framedRoot(crate)
	must(t, err)

	opts, err := jsonld()
	must(t, err)
	doc, err := ld.DocumentFromReader(bytes.NewReader(crate))
	must(t, err)
	var frame map[string]any
	must(t, json.Unmarshal(bronzeFrameJSON, &frame))
	fopts := opts.Copy()
	fopts.Embed = ld.EmbedAlways
	fopts.OmitGraph = true
	full, err := ld.NewJsonLdProcessor().Frame(doc, frame, fopts)
	must(t, err)
	delete(full, "@context")

	a, _ := json.Marshal(fast)
	b, _ := json.Marshal(full)
	if !bytes.Equal(a, b) {
		t.Fatalf("set aside:\n%s\nframed:\n%s", a, b)
	}
}

func TestRepublishingChecksOnlyTheNameAndDescriptionGiven(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	first := dataset(t, map[string]string{"a.txt": "a"})
	id := newID()
	pub, err := preparePublication(first, provenanceFlags{}, nil, id)
	must(t, err)
	must(t, upload(ctx, tg, pub))

	moved := filepath.Join(t.TempDir(), "renamed")
	must(t, os.Rename(first, moved))
	pub, err = preparePublication(moved, provenanceFlags{}, nil, id)
	must(t, err)
	if err := upload(ctx, tg, pub); err == nil || !strings.Contains(err.Error(), "renamed/a.txt: not in it") {
		t.Fatalf("a folder of another name holds other files, so it is another dataset: %v", err)
	}

	named, err := preparePublication(dataset(t, map[string]string{"a.txt": "a"}), provenanceFlags{name: "Run 1"}, nil, id)
	must(t, err)
	if err := upload(ctx, tg, named); err == nil || !strings.Contains(err.Error(), "name, description") {
		t.Fatalf("a name the first publish did not give was accepted: %v", err)
	}
	same, err := preparePublication(dataset(t, map[string]string{"a.txt": "a"}), provenanceFlags{}, nil, id)
	must(t, err)
	must(t, upload(ctx, tg, same))
}

const earlierLbfCrate = `{"@context": "https://w3id.org/ro/crate/1.2/context", "@graph": [
 {"@id": "./", "@type": "Dataset", "identifier": "20261005-vocal-unicorn-112d", "name": "20261005-vocal-unicorn-112d",
  "description": "Dataset 20261005-vocal-unicorn-112d", "license": "https://rightsstatements.org/vocab/InC/1.0/",
  "additionalProperty": [{"@id": "#source_path"}, {"@id": "#sample_id"}],
  "conformsTo": [{"@id": "https://w3id.org/ro/wfrun/process/0.5"}, {"@id": "https://github.com/ImperialCollegeLondon/lbf-data-tools/tree/main/profiles/bronze/0.4.0"}],
  "hasPart": [{"@id": "data/a.csv"}], "wasDerivedFrom": {"@id": "#source-20261005-crucial-lab-cccb"}},
 {"@id": "ro-crate-metadata.json", "@type": "CreativeWork", "about": {"@id": "./"}, "conformsTo": {"@id": "https://w3id.org/ro/crate/1.2"}},
 {"@id": "#source_path", "@type": "PropertyValue", "name": "source_path", "value": "/x/data"},
 {"@id": "#sample_id", "@type": "PropertyValue", "name": "sample_id", "value": "SAM-0001"},
 {"@id": "#source-20261005-crucial-lab-cccb", "@type": "Dataset", "identifier": "20261005-crucial-lab-cccb", "name": "20261005-crucial-lab-cccb"},
 {"@id": "https://example.org/pipe", "@type": "SoftwareApplication", "name": "pipe", "version": "0.1.0", "url": "https://example.org/pipe"},
 {"@id": "#run", "@type": "CreateAction", "endTime": "2026-10-05T15:43:27Z", "instrument": {"@id": "https://example.org/pipe"},
  "object": {"@id": "#source-20261005-crucial-lab-cccb"}, "result": {"@id": "./"}},
 {"@id": "data/a.csv", "@type": "File", "contentSize": "1"}]}`

func TestCratesFromAnEarlierLbfAreReadBack(t *testing.T) {
	p, schemas, err := crateStatement([]byte(earlierLbfCrate))
	must(t, err)
	want := provenance{DerivedFrom: "20261005-crucial-lab-cccb",
		Instruments: []instrument{{"pipe", "0.1.0", "https://example.org/pipe"}}, Properties: map[string]string{"sample_id": "SAM-0001"}}
	if !p.sameAs(want) || len(schemas) != 2 {
		t.Fatalf("got %+v %v", p, schemas)
	}
	if _, _, err := crateStatement([]byte(`{"@context": "https://example.org/other", "@graph": []}`)); err == nil {
		t.Fatal("a crate in an unknown context was read")
	}
}

func TestFetchReturnsWhatTheCrateStates(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	prov := provenanceFlags{derivedFrom: "20260101-x-y-0000", instruments: []instrument{{"pipe", "1", "https://example.org/pipe"}}, props: []string{"sample_id=S1"}, name: "Run 1"}
	pub, err := preparePublication(dataset(t, map[string]string{"a.csv": "x"}), prov, nil, "")
	must(t, err)
	must(t, upload(ctx, tg, pub))
	got, err := download(ctx, tg, pub.ID, t.TempDir())
	must(t, err)
	if got.stated == nil || got.stated.Name != "Run 1" || got.stated.DerivedFrom != "20260101-x-y-0000" || got.stated.Properties["sample_id"] != "S1" || len(got.stated.Instruments) != 1 {
		t.Fatalf("got %+v", got.stated)
	}
}

func TestProfileSiblingsThatCannotBeUsed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "profiles", "bronze", "profile.json"), string(bronzeProfileJSON))
	writeFile(t, filepath.Join(root, "profiles", "plate-read", "0.1.0", "profile.json"), profileJSON("plate-read", "0.1.0", bronzeProfileID(t), ""))
	if _, err := loadProfile(filepath.Join(root, "profiles", "plate-read", "0.1.0")); err != nil {
		t.Fatalf("a copy of bronze beside it: %v", err)
	}

	writeFile(t, filepath.Join(root, "profiles", "plate-copy", "0.1.0", "profile.json"), profileJSON("plate-read", "0.1.0", bronzeProfileID(t), `, "description": "edited"`))
	if _, err := loadProfile(filepath.Join(root, "profiles", "plate-read", "0.1.0")); err == nil || !strings.Contains(err.Error(), "both have the $id") {
		t.Fatalf("two differing profiles with one $id: %v", err)
	}

	other := t.TempDir()
	writeFile(t, filepath.Join(other, "profiles", "base", "0.1.0", "profile.json"), `{"$id": "https://w3id.org/lbf/profiles/base/0.1.0", `)
	writeFile(t, filepath.Join(other, "profiles", "top", "0.1.0", "profile.json"), profileJSON("top", "0.1.0", "https://w3id.org/lbf/profiles/base/0.1.0", ""))
	if _, err := loadProfile(filepath.Join(other, "profiles", "top", "0.1.0")); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("a parent that cannot be read: %v", err)
	}
}

func TestPropertyNamesAreSafeInTheCrate(t *testing.T) {
	for _, name := range []string{"@none", "profile-plate-read-0.1.0-schema"} {
		if _, err := (provenanceFlags{props: []string{name + "=x"}}).load(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	prof, err := loadProfile("")
	must(t, err)
	pub := publication{ID: newID(), Source: "/data/run1", Files: []localFile{{Rel: "run1/a", Size: 1}},
		Provenance: provenance{Properties: map[string]string{"plate id": "P 1", "a/b": "c"}}, Profile: prof}
	crate, err := pub.crate(target{User: "u"})
	must(t, err)
	if !strings.Contains(string(crate), `"@id": "#plate%20id"`) {
		t.Fatalf("property id not encoded:\n%s", crate)
	}
	stated, _, err := crateStatement(crate)
	must(t, err)
	if stated.Properties["plate id"] != "P 1" || stated.Properties["a/b"] != "c" {
		t.Fatalf("read back %v", stated.Properties)
	}
}

func TestADatasetCanMeetSeveralProfiles(t *testing.T) {
	root := t.TempDir()
	bronze := bronzeProfileID(t)
	lab := filepath.Join(root, "profiles", "plate-read", "0.1.0")
	project := filepath.Join(root, "profiles", "amr-project", "0.1.0")
	writeFile(t, filepath.Join(lab, "profile.json"), profileJSON("plate-read", "0.1.0", bronze, `, "properties": {"additionalProperty": {"required": ["plate_id"]}}`))
	writeFile(t, filepath.Join(project, "profile.json"), profileJSON("amr-project", "0.1.0", bronze, `, "properties": {"additionalProperty": {"required": ["grant"]}}`))
	data := dataset(t, map[string]string{"a.csv": "x"})

	pub, err := preparePublication(data, provenanceFlags{props: []string{"plate_id=P-0001", "grant=G1"}}, []string{lab, project}, "")
	must(t, err)
	crate, err := pub.crate(target{User: "u"})
	must(t, err)
	_, schemas, err := crateStatement(crate)
	must(t, err)
	if len(schemas) != 3 || schemas["https://w3id.org/lbf/profiles/amr-project/0.1.0"] == "" || schemas["https://w3id.org/lbf/profiles/plate-read/0.1.0"] == "" {
		t.Fatalf("conformsTo %v", slices.Sorted(maps.Keys(schemas)))
	}

	pub, err = preparePublication(data, provenanceFlags{props: []string{"plate_id=P-0001"}}, []string{lab, project}, "")
	must(t, err)
	if _, err := pub.crate(target{User: "u"}); err == nil || !strings.Contains(err.Error(), "missing --property grant=...") {
		t.Fatalf("one profile's rules skipped: %v", err)
	}
}

func loaded(t *testing.T, dir string) *profile {
	t.Helper()
	prof, err := loadProfile(dir)
	must(t, err)
	return prof
}

const strictProfile = `{"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "https://w3id.org/lbf/profiles/strict/0.1.0",
 "$ref": "https://w3id.org/lbf/profiles/bronze/0.5.0", "title": "Strict", "required": ["keywords"],
 "properties": {
  "name": {"title": "Plate name", "pattern": "^Plate ", "examples": ["Plate P-0001"]},
  "description": {"minLength": 400},
  "keywords": {"title": "Keywords"},
  "hasPart": {"description": "A FASTA file", "contains": {"properties": {"@id": {"pattern": "\\.fasta$"}}}}}}`

func TestProfileErrorsOnTheRootSayWhatFixesThem(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "strict", "0.1.0")
	writeFile(t, filepath.Join(dir, "profile.json"), strictProfile)
	pub, err := preparePublication(dataset(t, map[string]string{"a.csv": "x"}), provenanceFlags{}, []string{dir}, "")
	must(t, err)
	_, err = pub.crate(target{User: "u"})
	for _, want := range []string{
		"does not meet profile Strict (https://w3id.org/lbf/profiles/strict/0.1.0)",
		"is not in the form the profile asks for\n      Plate name; e.g. Plate P-0001",
		"  --description: is ",
		"characters long, but must be at least 400",
		"  missing keywords, which no flag of lbf's gives, so no dataset can meet this profile",
		"  files: no file is what the profile asks for\n      A FASTA file",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("lacks %q: %v", want, err)
		}
	}

	pub, err = preparePublication(dataset(t, map[string]string{"a.csv": "x"}), provenanceFlags{}, nil, "")
	must(t, err)
	if _, err := pub.crate(target{}); err == nil || !strings.Contains(err.Error(), "missing creator: lbf names whoever signed in") {
		t.Errorf("no uploader: %v", err)
	}
}

func TestShowSaysWhatNoFlagCanGive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "strict", "0.1.0")
	writeFile(t, filepath.Join(dir, "profile.json"), strictProfile)
	got := map[string]ask{}
	for _, a := range loaded(t, dir).asks() {
		got[a.flag] = a
	}
	for flag, want := range map[string]ask{
		"--name ...":        {"--name ...", "required", "Plate name; e.g. Plate P-0001"},
		"--description ...": {"--description ...", "required", "lbf's own does not meet it"},
		"keywords":          {"keywords", "required", "no flag of lbf's gives it, so no dataset can meet this profile"},
		"files":             {"files", "required", "A FASTA file"},
	} {
		if got[flag] != want {
			t.Errorf("%s: got %+v", flag, got[flag])
		}
	}
	if _, ok := got["license"]; ok {
		t.Errorf("a term lbf sets is asked for: %+v", got)
	}
	shown := describeProfile(loaded(t, dir), dir)
	for _, want := range []string{"Strict", "Builds on https://w3id.org/lbf/profiles/bronze/0.5.0", "lbf publish <folder> --profile " + dir + " --name ... --description ..."} {
		if !strings.Contains(shown, want) {
			t.Errorf("show lacks %q:\n%s", want, shown)
		}
	}
}
