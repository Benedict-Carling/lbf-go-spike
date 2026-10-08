package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
