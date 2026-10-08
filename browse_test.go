package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestFlattenCrateShowsWhatPublishRecorded(t *testing.T) {
	prov := provenance{
		DerivedFrom: "20260101-x-y-0000",
		Instruments: []instrument{{"CellProfiler", "4.2.6", "https://cellprofiler.org"}},
		Properties:  map[string]string{"sample_id": "S1"},
	}
	files := []localFile{{Rel: "run1/a.csv", Size: 3, SHA256: "abc"}, {Rel: "run1/b.csv", Size: 4}}
	raw, err := publication{ID: "20260102-a-b-1234", Source: "/data/run1", Files: files, Provenance: prov}.buildCrate(target{User: "ann@example.org"}, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	must(t, err)

	row, got, names, err := flattenCrate(raw)
	must(t, err)
	if row["run_instrument"] != "CellProfiler 4.2.6" || len(names) != 1 || names[licenseURL] != "In Copyright" {
		t.Errorf("run_instrument: got %v, names %v", row["run_instrument"], names)
	}
	for k, want := range map[string]any{
		"identifier":    "20260102-a-b-1234",
		"creator":       "ann@example.org",
		"datePublished": "2026-01-02T03:04:05Z",
		"sample_id":     "S1",
		"files":         2,
		"size_bytes":    int64(7),
	} {
		if row[k] != want {
			t.Errorf("%s: got %v (%T), want %v", k, row[k], row[k], want)
		}
	}
	if len(got) != 2 || got[0].Path != fileID("run1/a.csv") || got[0].SHA256 != "abc" {
		t.Errorf("files: got %+v", got)
	}
}

func TestFlattenCrateShowsFieldsNoOneTaughtIt(t *testing.T) {
	raw, err := publication{ID: "20260102-a-b-1234", Source: "/data/run1", Files: []localFile{{Rel: "run1/a", Size: 1}}, Provenance: provenance{Properties: map[string]string{}}}.buildCrate(target{}, time.Now())
	must(t, err)
	var doc map[string]any
	must(t, json.Unmarshal(raw, &doc))
	graph := doc["@graph"].([]any)
	for _, e := range graph {
		if e.(map[string]any)["@id"] == "./" {
			e.(map[string]any)["keywords"] = "screen"
			e.(map[string]any)["isPartOf"] = map[string]any{"@id": "#cycle-3"}
		}
	}
	doc["@graph"] = append(graph, map[string]any{"@id": "#cycle-3", "@type": "Collection", "name": "Cycle 3"})
	raw, err = json.Marshal(doc)
	must(t, err)

	row, _, _, err := flattenCrate(raw)
	must(t, err)
	if row["keywords"] != "screen" || row["isPartOf"] != "Cycle 3" {
		t.Fatalf("got %v", row)
	}
}

func TestFlattenCrateKeysLinksByIRIUnlessTheVersionIsNotInIt(t *testing.T) {
	raw := []byte(`{"@graph": [
		{"@id": "ro-crate-metadata.json", "about": {"@id": "./"}},
		{"@id": "./", "@type": "Dataset", "conformsTo": {"@id": "https://w3id.org/ro/wfrun/process/0.5"}, "author": {"@id": "mailto:a@example.org"}},
		{"@id": "https://w3id.org/ro/wfrun/process/0.5", "name": "Process Run Crate", "version": "0.5"},
		{"@id": "mailto:a@example.org", "@type": "Person", "name": "Ann"}
	]}`)
	row, _, names, err := flattenCrate(raw)
	must(t, err)
	if row["conformsTo"] != "https://w3id.org/ro/wfrun/process/0.5" || names["https://w3id.org/ro/wfrun/process/0.5"] != "Process Run Crate 0.5" {
		t.Errorf("conformsTo: got %v named %q", row["conformsTo"], names["https://w3id.org/ro/wfrun/process/0.5"])
	}
	if row["author"] != "mailto:a@example.org" || names["mailto:a@example.org"] != "Ann" {
		t.Errorf("author: got %v named %q", row["author"], names["mailto:a@example.org"])
	}
}

func TestFlattenCrateKeepsEachValueOfAMultiValuedProperty(t *testing.T) {
	raw, err := publication{ID: "20260102-a-b-1234", Source: "/data/run1", Files: []localFile{{Rel: "run1/a", Size: 1}}, Provenance: provenance{Properties: map[string]string{}}, Profile: &profile{rules: []rule{{id: "https://example.org/p1", raw: []byte("{}")}, {id: "https://example.org/p2", raw: []byte("{}")}}}}.buildCrate(target{}, time.Now())
	must(t, err)
	row, _, _, err := flattenCrate(raw)
	must(t, err)
	got, ok := row["conformsTo"].([]string)
	if !ok || !slices.Contains(got, "https://example.org/p1") || !slices.Contains(got, "https://example.org/p2") {
		t.Fatalf("got %#v", row["conformsTo"])
	}
}

func TestSyncCratesCachesCratesAndAsksAgainForTheRest(t *testing.T) {
	tg := emulator(t, uploadPerms)
	cc, err := tg.client()
	must(t, err)
	ctx := context.Background()
	put := func(name string) {
		_, err := cc.NewBlockBlobClient(name).UploadBuffer(ctx, []byte(`{}`), nil)
		must(t, err)
	}
	put("done/" + crateName)
	put("done/a.csv")
	put("partway/a.csv")
	cache := t.TempDir()

	ids, err := syncCrates(ctx, cc, cache)
	must(t, err)
	if !slices.Equal(ids, []string{"done", "partway"}) {
		t.Fatalf("ids: %v", ids)
	}
	if _, err := os.Stat(filepath.Join(cache, "done", crateName)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cache, "partway")); !os.IsNotExist(err) {
		t.Fatalf("cached a folder with no crate: %v", err)
	}

	put("partway/" + crateName)
	_, err = syncCrates(ctx, cc, cache)
	must(t, err)
	if _, err := os.Stat(filepath.Join(cache, "partway", crateName)); err != nil {
		t.Fatal("a crate that landed later was not picked up")
	}
}

func TestFetchCommandOmitsDefaults(t *testing.T) {
	for _, c := range []struct{ container, tag, want string }{
		{"bronze", "tag=storage", "lbf fetch X --account acc"},
		{"silver", "tag=storage-test", "lbf fetch X --container silver --tag tag=storage-test --account acc"},
	} {
		if got := fetchCommand("X", c.container, c.tag, "acc"); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
