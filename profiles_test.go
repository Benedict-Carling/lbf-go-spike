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

func TestRegistryRef(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(t, filepath.Join("plate-local", "profile.json"), "{}")
	must(t, os.MkdirAll(filepath.Join("plate-read", "0.1.0"), 0o755))
	for arg, want := range map[string][2]string{
		"plate-read":       {"plate-read", ""},
		"plate-read@0.2.0": {"plate-read", "0.2.0"},
		"plate-read@v1":    {"plate-read", "v1"},
	} {
		ref, ok := publishedRef(arg)
		if !ok || ref.name != want[0] || ref.version != want[1] {
			t.Errorf("%s: got %v %v", arg, ref, ok)
		}
	}
	for _, arg := range []string{"", "plate-local", "./plate-read", `profiles\plate-read`, "Plate-Read", "plate-read@latest"} {
		if _, ok := publishedRef(arg); ok {
			t.Errorf("%q taken as a published profile", arg)
		}
	}
}

func profileJSON(name, version, parent, extra string) string {
	return `{"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "https://w3id.org/lbf/profiles/` + name + `/` + version +
		`", "$ref": "` + parent + `", "title": "` + name + `"` + extra + `}`
}

func TestRegistryPublishesResolvesShowsAndPulls(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	cache := t.TempDir()
	cacheRoot = func() (string, error) { return cache, nil }
	t.Cleanup(func() { cacheRoot = os.UserCacheDir })

	bronze := bronzeProfileID(t)
	src := t.TempDir()
	plate := `, "required": ["additionalProperty"], "properties": {"additionalProperty": {"required": ["plate_id"], "properties": {"plate_id": {"title": "Plate barcode", "examples": ["P-0001"]}}}}`
	writeFile(t, filepath.Join(src, "plate-read", "0.1.0", "profile.json"), profileJSON("plate-read", "0.1.0", bronze, plate))
	writeFile(t, filepath.Join(src, "plate-read", "0.2.0", "profile.json"), profileJSON("plate-read", "0.2.0", bronze, plate))
	writeFile(t, filepath.Join(src, "plate-summary", "0.1.0", "profile.json"), profileJSON("plate-summary", "0.1.0", "https://w3id.org/lbf/profiles/plate-read/0.2.0", ""))

	if _, err := publishProfileTo(ctx, tg, loaded(t, filepath.Join(src, "plate-summary", "0.1.0"))); err == nil || !strings.Contains(err.Error(), "publish it first") {
		t.Fatalf("published before what it builds on: %v", err)
	}
	for _, v := range []string{"0.1.0", "0.2.0"} {
		msg, err := publishProfileTo(ctx, tg, loaded(t, filepath.Join(src, "plate-read", v)))
		must(t, err)
		t.Log(msg)
	}
	msg, err := publishProfileTo(ctx, tg, loaded(t, filepath.Join(src, "plate-read", "0.2.0")))
	if err != nil || !strings.Contains(msg, "unchanged") {
		t.Fatalf("republishing the same profile: %s %v", msg, err)
	}
	writeFile(t, filepath.Join(src, "plate-read", "0.2.0", "profile.json"), profileJSON("plate-read", "0.2.0", bronze, ""))
	if _, err := publishProfileTo(ctx, tg, loaded(t, filepath.Join(src, "plate-read", "0.2.0"))); err == nil || !strings.Contains(err.Error(), "never changes") {
		t.Fatalf("a published version was replaced: %v", err)
	}
	if _, err := publishProfileTo(ctx, tg, loaded(t, filepath.Join(src, "plate-summary", "0.1.0"))); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("published on top of a local copy that differs from the published one: %v", err)
	}

	latest, err := publishedProfile(ctx, tg, published{"plate-read", ""})
	must(t, err)
	if filepath.Base(latest) != "0.2.0" {
		t.Fatalf("latest is %s", latest)
	}
	if _, err := publishedProfile(ctx, tg, published{"plate-read", "0.3.0"}); err == nil || !strings.Contains(err.Error(), "0.1.0, 0.2.0") {
		t.Fatalf("missing version: %v", err)
	}
	prof, err := loadProfile(latest)
	must(t, err)
	shown := describeProfile(prof, "plate-read@0.2.0")
	for _, want := range []string{"--property plate_id=...", "required", "Plate barcode; e.g. P-0001", "lbf publish <folder> --profile plate-read@0.2.0 --property plate_id=..."} {
		if !strings.Contains(shown, want) {
			t.Errorf("show lacks %q:\n%s", want, shown)
		}
	}

	pulled, err := writeProfiles(prof, t.TempDir())
	must(t, err)
	data := dataset(t, map[string]string{"readings.csv": "well,abs\nA1,0.5\n"})
	pub, err := preparePublication(data, provenanceFlags{props: []string{"plate_id=P-0001"}}, []string{pulled}, "")
	must(t, err)
	if _, err := pub.crate(tg); err != nil {
		t.Fatalf("a published profile does not check a dataset: %v", err)
	}
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

func TestDryRunUsesACachedProfileWithoutSigningIn(t *testing.T) {
	cache := t.TempDir()
	cacheRoot = func() (string, error) { return cache, nil }
	t.Cleanup(func() { cacheRoot = os.UserCacheDir })
	dir := filepath.Join(cache, "lbf", "profiles", "someaccount", "plate-read", "0.1.0")
	writeFile(t, filepath.Join(dir, "profile.json"), profileJSON("plate-read", "0.1.0", bronzeProfileID(t), ""))

	got, err := profileDir(context.Background(), options{}, "plate-read@0.1.0", true)
	if err != nil || got != dir {
		t.Fatalf("got %s %v", got, err)
	}
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

func TestEmptyVersionFoldersAreNotPublished(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plate-read", "0.1.0", "profile.json"), "{}")
	must(t, os.MkdirAll(filepath.Join(dir, "plate-read", "0.9.0"), 0o755))
	if got := publishedVersions(dir, "plate-read"); len(got) != 1 || got[0] != "0.1.0" {
		t.Fatalf("got %v", got)
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

func TestProfileVersionsSortAsSemanticVersions(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []string{"0.10.0", "0.9.0", "v1", "0.9", "1.0.0.1"} {
		writeFile(t, published{"plate-read", v}.in(dir), "{}")
	}
	if got := publishedVersions(dir, "plate-read"); !slices.Equal(got, []string{"0.9", "0.9.0", "0.10.0", "v1"}) {
		t.Fatalf("got %v", got)
	}
}
