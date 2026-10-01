package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDatasetFilesLayoutAndExclusions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "run1")
	for _, p := range []string{"a.txt", "sub/b.txt", ".DS_Store", "sub/._b.txt"} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(p)), "x")
	}
	_ = os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "link.txt"))

	_, files, err := datasetFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Rel)
	}
	if want := "run1/a.txt,run1/sub/b.txt"; strings.Join(got, ",") != want {
		t.Fatalf("got %v, want %s", got, want)
	}

	_, single, err := datasetFiles(filepath.Join(root, "a.txt"))
	if err != nil || len(single) != 1 || single[0].Rel != "a.txt" {
		t.Fatalf("single file: %v %v", single, err)
	}
}

func TestSASEnvRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "azure_sas.env")
	in := target{
		Account: "examplestorage1", Container: "bronze", SAS: "sv=x&sig=y",
		Expiry: time.Now().Add(time.Hour).UTC().Truncate(time.Second), Permissions: "rl",
		User: "u@example.com", SubscriptionName: "Sub", SubscriptionID: "id",
	}
	if err := writeSASEnv(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := readSASEnv(path, "download", "bronze")
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %+v, want %+v", out, in)
	}
	if _, err := readSASEnv(path, "upload", "bronze"); err == nil {
		t.Fatal("read-only credential accepted for upload")
	}
	if _, err := readSASEnv(path, "download", "silver"); err == nil {
		t.Fatal("credential for another container accepted")
	}
}

func TestSASEnvRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "azure_sas.env")
	writeFile(t, path, "PATH='/tmp'\n")
	if _, err := readSASEnv(path, "download", "bronze"); err == nil || !strings.Contains(err.Error(), "unexpected key") {
		t.Fatalf("got %v", err)
	}
}

func TestCreateInRejectsEscapes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "out")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := []string{"../x", "run1/../../x"}
	// Windows runners may lack the privilege to create symlinks.
	if os.Symlink(base, filepath.Join(root, "link")) == nil {
		bad = append(bad, "link/x")
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	fh, err := createIn(dir, "run1/sub/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	fh.Close()
	for _, name := range bad {
		if fh, err := createIn(dir, name); err == nil {
			fh.Close()
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRepairWindowsArgs(t *testing.T) {
	got := repairWindowsArgs([]string{"publish", `.\test data" --dry-run  --tag role=x`, "--out", `C:\d"`})
	want := []string{"publish", `.\test data\`, "--dry-run", "--tag", "role=x", "--out", `C:\d\`}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestCrateMatchesPythonShape(t *testing.T) {
	files := []localFile{{Rel: "run1/a b#1%.csv", Size: 5}, {Rel: "run1/a.txt", Size: 3}}
	tgt := target{Account: "acct", Container: "bronze", User: "u", SubscriptionName: "S", SubscriptionID: "I"}
	raw, err := buildCrate("20260101-x-y-0000", "/data/run1", files, tgt, provenance{}, nil, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Context string           `json:"@context"`
		Graph   []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Context != "https://w3id.org/ro/crate/1.2/context" || doc.Graph[0]["datePublished"] != "2026-01-01T00:00:00Z" {
		t.Fatalf("unexpected crate: %s", raw)
	}
	last := doc.Graph[len(doc.Graph)-1]
	if last["@id"] != "run1/a.txt" || last["contentSize"] != "3" {
		t.Fatalf("file entity: %v", last)
	}
	if !strings.Contains(string(raw), `"run1/a%20b%231%25.csv"`) || strings.Contains(string(raw), "a b#1") {
		t.Fatalf("file @id not percent-encoded like ro-crate-py: %s", raw)
	}
}

func TestProfileCannotReplaceBronze(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "fake", "profile.json"),
		`{"$id": "https://github.com/ImperialCollegeLondon/lbf-data-tools/tree/main/profiles/bronze/0.4.0"}`)
	if _, err := loadProfile(filepath.Join(root, "fake")); err == nil || !strings.Contains(err.Error(), "bronze") {
		t.Fatalf("profile reusing bronze's $id accepted: %v", err)
	}
}

func TestTransferStopsStartingAfterAFailure(t *testing.T) {
	var calls atomic.Int64
	err := transferAll(t.Context(), make([]int, 100), func(context.Context, int) (string, error) {
		calls.Add(1)
		return "", errors.New("boom")
	})
	if err == nil || calls.Load() > parallelFiles {
		t.Fatalf("err %v after %d calls", err, calls.Load())
	}
}

func TestSignatureRedaction(t *testing.T) {
	msg := `Get "https://a.blob.core.windows.net/c?sp=rl&sig=abc%2Bdef&sv=1": tls error`
	got := sasSignature.ReplaceAllString(msg, "sig=REDACTED")
	if strings.Contains(got, "abc") || !strings.Contains(got, "sig=REDACTED&sv=1") {
		t.Fatal(got)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const minimalSilverProfile = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.org/profiles/minimal-silver",
  "$ref": "https://github.com/ImperialCollegeLondon/lbf-data-tools/tree/main/profiles/bronze/0.4.0",
  "properties": {
    "crate": {
      "required": ["wasDerivedFrom", "instrument"],
      "properties": {"additionalProperty": {"required": ["sample_id"]}}
    }
  }
}`

const cellPaintingProfile = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.org/profiles/cell-painting",
  "$ref": "https://example.org/profiles/minimal-silver",
  "properties": {
    "data": {"properties": {"files": {"allOf": [
      {"contains": {"properties": {"path": {"const": "plate_map.csv"}}}},
      {"contains": {"properties": {"path": {"pattern": "^features/.+\\.parquet$"}}}},
      {"items": {"properties": {"path": {"pattern": "^(plate_map\\.csv|features/.+|qc/.+)$"}}}}
    ]}}},
    "crate": {"properties": {
      "instrument": {"contains": {"properties": {"name": {"const": "CellProfiler"}}}},
      "additionalProperty": {"required": ["plate_id"]}
    }}
  }
}`

func cellPaintingRun() publication {
	return publication{
		ID:     newID(),
		Source: "/data/run1",
		Files: []localFile{
			{Rel: "run1/plate_map.csv", Size: 10},
			{Rel: "run1/features/P001.parquet", Size: 100},
		},
		Provenance: provenance{
			DerivedFrom: "20260101-x-y-0000",
			Instruments: []instrument{{"CellProfiler", "4.2.6", "https://cellprofiler.org"}},
			Properties:  map[string]string{"sample_id": "S1", "plate_id": "P001"},
		},
	}
}

func TestProfileChecksDataAndCrate(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "minimal-silver", "profile.json"), minimalSilverProfile)
	writeFile(t, filepath.Join(root, "cell-painting", "profile.json"), cellPaintingProfile)
	prof, err := loadProfile(filepath.Join(root, "cell-painting"))
	if err != nil {
		t.Fatal(err)
	}
	tgt := target{Account: "acct", Container: "silver", User: "u@example.org"}

	good := cellPaintingRun()
	good.Profile = prof
	crate, err := good.crate(tgt)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/profiles/bronze/0.4.0", "/profiles/minimal-silver", "/profiles/cell-painting", processRunCrate,
		`"wasDerivedFrom": {`, `"#source-20260101-x-y-0000"`, `"CreateAction"`, `"name": "plate_id"`, `"name": "u@example.org"`,
	} {
		if !strings.Contains(string(crate), want) {
			t.Errorf("crate missing %s", want)
		}
	}

	cases := map[string]func(*publication, *target){
		"stray file":   func(p *publication, _ *target) { p.Files = append(p.Files, localFile{Rel: "run1/notes.txt", Size: 1}) },
		"no plate map": func(p *publication, _ *target) { p.Files = p.Files[1:] },
		"no plate_id":  func(p *publication, _ *target) { p.Provenance.Properties = map[string]string{"sample_id": "S1"} },
		"no sample_id": func(p *publication, _ *target) { p.Provenance.Properties = map[string]string{"plate_id": "P001"} },
		"wrong instrument": func(p *publication, _ *target) {
			p.Provenance.Instruments = []instrument{{"Fiji", "2", "https://fiji.sc"}}
		},
		"no parent":        func(p *publication, _ *target) { p.Provenance.DerivedFrom, p.Provenance.Instruments = "", nil },
		"unknown uploader": func(_ *publication, t *target) { t.User = "" },
	}
	for name, change := range cases {
		pub, tg := cellPaintingRun(), tgt
		pub.Profile = prof
		change(&pub, &tg)
		if _, err := pub.crate(tg); err == nil {
			t.Errorf("%s: accepted", name)
		} else {
			t.Logf("%s:\n%v", name, err)
		}
	}
}

func TestBronzeAppliesUnderAnyProfile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "loose", "profile.json"), `{"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "https://example.org/loose"}`)
	prof, err := loadProfile(filepath.Join(root, "loose"))
	if err != nil {
		t.Fatal(err)
	}
	pub := publication{ID: newID(), Source: "/data/a", Files: []localFile{{Rel: "a", Size: 1}}, Profile: prof}
	_, err = pub.crate(target{Container: "bronze"})
	if err == nil || !strings.Contains(err.Error(), "/profiles/bronze/") || !strings.Contains(err.Error(), "creator") {
		t.Fatalf("bronze rules skipped: %v", err)
	}
}

func TestProvenanceRules(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "a.txt"), "a")
	cases := map[string]string{
		"unknown field":          `{"properties": {}, "instrumentz": []}`,
		"parent without tools":   `{"derived_from": "20260101-x-y-0000"}`,
		"tools without parent":   `{"instruments": [{"name": "a", "version": "1", "url": "u"}]}`,
		"incomplete instrument":  `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a"}]}`,
		"bad parent id":          `{"derived_from": "../x", "instruments": [{"name": "a", "version": "1", "url": "https://a"}], "properties": {}}`,
		"reserved property":      `{"properties": {"source_path": "/elsewhere"}}`,
		"property named run":     `{"properties": {"run": "7"}}`,
		"property uploader":      `{"properties": {"uploader": "x"}}`,
		"property blob-location": `{"properties": {"blob-location": "x"}}`,
		"property source-":       `{"properties": {"source-20260101-x-y-0000": "x"}}`,
		"non-string property":    `{"properties": {"n": 1}}`,
		"trailing content":       `{"properties": {}} {"derived_from": "x"}`,
		"url not a uri":          `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "not a url"}], "properties": {}}`,
	}
	for name, body := range cases {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
		writeFile(t, path, body)
		_, err := preparePublication(data, path, "")
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		t.Logf("%s:\n%v", name, err)
	}
}

func TestProvenanceAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	writeFile(t, path, `{"derived_from": "20260101-x-y-0000",
  "instruments": [{"name": "CellProfiler", "version": "4.2.6", "url": "https://cellprofiler.org"}]}`)
	p, err := readProvenance(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.DerivedFrom != "20260101-x-y-0000" || len(p.Instruments) != 1 || p.Properties == nil {
		t.Fatalf("%+v", p)
	}
}

func TestDefaultsAreBronze(t *testing.T) {
	prov, err := readProvenance("")
	if err != nil || prov.DerivedFrom != "" || prov.Properties == nil {
		t.Fatalf("default provenance: %+v %v", prov, err)
	}
	prof, err := loadProfile("")
	if err != nil || !strings.Contains(prof.ID, "/profiles/bronze/") {
		t.Fatalf("default profile: %v %v", prof, err)
	}
	tgt := target{Container: "bronze", User: "u"}
	pub := publication{ID: newID(), Source: "/data/a", Files: []localFile{{Rel: "a", Size: 1}}, Provenance: prov, Profile: prof}
	crate, err := pub.crate(tgt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(crate), prof.ID) {
		t.Fatalf("crate does not record %s", prof.ID)
	}
	if strings.Contains(string(crate), processRunCrate) {
		t.Fatal("bronze crate claims a process run")
	}
	pub.Files = nil
	if _, err := pub.crate(tgt); err == nil {
		t.Fatal("bronze accepted a dataset with no files")
	}
}
