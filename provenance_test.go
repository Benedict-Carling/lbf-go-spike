package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestInstrumentsSharingAURLMustBeTheSame(t *testing.T) {
	align := instrument{"align", "1.0", "https://example.org/pipe"}
	qc := instrument{"qc", "2.0", "https://example.org/pipe"}
	newer := instrument{"align", "1.1", "https://example.org/pipe"}
	for name, tools := range map[string][]instrument{"other name": {align, qc}, "other version": {align, newer}} {
		_, err := provenanceFlags{derivedFrom: "20260101-x-y-0000", instruments: tools}.load()
		if err == nil || !strings.Contains(err.Error(), "https://example.org/pipe") {
			t.Errorf("%s: %v", name, err)
		}
	}

	repeated := provenance{Name: "Run 1", Description: "Aligned", DerivedFrom: "20260101-x-y-0000", Instruments: []instrument{align, align}, Properties: map[string]string{}}
	if err := repeated.check(); err != nil {
		t.Fatal(err)
	}
	prof, err := loadProfile("")
	must(t, err)
	pub := publication{ID: newID(), Source: "/data/run1", Files: []localFile{{Rel: "run1/a", Size: 1}}, Provenance: repeated, Profile: prof}
	raw, err := pub.crate(target{Container: "bronze", User: "u"})
	must(t, err)
	got, _, err := crateStatement(raw)
	must(t, err)
	if !got.sameAs(repeated) {
		t.Fatalf("read back %+v", got)
	}
}

func TestProfileIDMustBeAnAbsoluteURL(t *testing.T) {
	for _, id := range []string{"./", "run1/a.txt", crateName, "#uploader", "profiles/x", "https://example.org/p#part", "urn:example:p", "file:///p"} {
		dir := filepath.Join(t.TempDir(), "p")
		writeFile(t, filepath.Join(dir, "profile.json"), `{"$id": "`+id+`"}`)
		_, err := loadProfile(dir)
		if err == nil || !strings.Contains(err.Error(), "absolute URL") || !strings.Contains(err.Error(), filepath.Join(dir, "profile.json")) {
			t.Errorf("%q: %v", id, err)
		}
	}
	dir := filepath.Join(t.TempDir(), "p")
	writeFile(t, filepath.Join(dir, "profile.json"), `{"$id": "https://example.org/p"}`)
	if _, err := loadProfile(dir); err != nil {
		t.Fatal(err)
	}
}

func TestProfileSiblingsComeOnlyFromTheProfilesFolder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "proj", "cfg", "profile.json"), `{"$id": "./"}`)
	writeFile(t, filepath.Join(root, "proj", "1.0", "profile.json"), `{"$id": "./"}`)
	writeFile(t, filepath.Join(root, "profiles", "notes", "profile.json"), `{"$id": "notes"}`)
	writeFile(t, filepath.Join(root, "profiles", "zz", "cfg", "profile.json"), `{"$id": "https://example.org/profiles/minimal-silver", "type": "nonsense"}`)
	writeFile(t, filepath.Join(root, "profiles", "minimal-silver", "0.1.0", "profile.json"), minimalSilverProfile)
	writeFile(t, filepath.Join(root, "profiles", "cell-painting", "profile.json"), cellPaintingProfile)
	prof, err := loadProfile(filepath.Join(root, "profiles", "cell-painting"))
	if err != nil {
		t.Fatal(err)
	}
	if got := prof.ids(); len(got) != 3 || !strings.HasSuffix(got[1], "/minimal-silver") {
		t.Fatalf("chain %v", got)
	}

	writeFile(t, filepath.Join(root, "profiles", "broken", "0.1.0", "profile.json"), `{"$id": "https://example.org/profiles/broken", "type": 5}`)
	_, err = loadProfile(filepath.Join(root, "profiles", "broken", "0.1.0"))
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "profiles", "broken", "0.1.0", "profile.json")) {
		t.Fatalf("error does not name the file: %v", err)
	}
}

func TestProvenanceFilesAreReadExactly(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "a.txt"), "a")
	tool := `[{"name": "a", "version": "1", "url": "https://a"}]`
	for name, body := range map[string]string{
		"trailing brace":       `{"properties": {"sample_id": "S1"}}}`,
		"trailing bracket":     `{"properties": {}}]`,
		"mis-cased field":      `{"Derived_From": "20260101-x-y-0000", "instruments": ` + tool + `}`,
		"upper-case field":     `{"derived_from": "20260101-x-y-0000", "INSTRUMENTS": ` + tool + `}`,
		"mis-cased tool field": `{"derived_from": "20260101-x-y-0000", "instruments": [{"Name": "a", "version": "1", "url": "https://a"}]}`,
		"duplicate field":      `{"properties": {}, "properties": {"a": "b"}}`,
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
		writeFile(t, path, body)
		if _, err := preparePublication(data, provenanceFlags{file: path}, nil, ""); err == nil {
			t.Errorf("%s: accepted", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
	for name, body := range map[string]string{"trailing bracket": `{"sample_id": "S1"}]`, "trailing brace": `{"sample_id": "S1"}}`} {
		path := filepath.Join(dir, "props_"+strings.ReplaceAll(name, " ", "_")+".json")
		writeFile(t, path, body)
		if _, err := (provenanceFlags{properties: path}).load(); err == nil {
			t.Errorf("properties %s: accepted", name)
		}
	}
	mixed := filepath.Join(dir, "mixed.json")
	writeFile(t, mixed, `{"Sample_ID": "S1", "sample_id": "S2"}`)
	if p, err := (provenanceFlags{properties: mixed}).load(); err != nil || p.Properties["Sample_ID"] != "S1" || p.Properties["sample_id"] != "S2" {
		t.Errorf("property names are the user's own: %+v %v", p, err)
	}

	bom := filepath.Join(dir, "bom.json")
	writeFile(t, bom, "\uFEFF"+`{"derived_from": "20260101-x-y-0000", "instruments": `+tool+`}`)
	if p, err := readProvenance(bom); err != nil || p.DerivedFrom != "20260101-x-y-0000" {
		t.Errorf("byte-order mark: %+v %v", p, err)
	}
	bomProps := filepath.Join(dir, "bom-props.json")
	writeFile(t, bomProps, "\uFEFF"+`{"sample_id": "S1"}`)
	if p, err := (provenanceFlags{properties: bomProps}).load(); err != nil || p.Properties["sample_id"] != "S1" {
		t.Errorf("byte-order mark in properties: %+v %v", p, err)
	}

	unknown := filepath.Join(dir, "unknown.json")
	writeFile(t, unknown, `{"properties": {}, "Derived_From": "x"}`)
	if _, err := readProvenance(unknown); err == nil || !strings.Contains(err.Error(), `unknown field "Derived_From"`) {
		t.Errorf("unknown field message: %v", err)
	}
}

func TestDerivedFromMustBeAMintedID(t *testing.T) {
	tool := []instrument{{"a", "1", "https://a"}}
	for _, id := range []string{"my-dataset", "run1", "20260101-x-y-000", "20260101-X-y-0000", "20260101-x-y-0000.v2"} {
		if _, err := (provenanceFlags{derivedFrom: id, instruments: tool}).load(); err == nil || !strings.Contains(err.Error(), id) {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range []string{"20260101-x-y-0000", "20261001-fancy-dassie-eadb", newID()} {
		if _, err := (provenanceFlags{derivedFrom: id, instruments: tool}).load(); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
}
