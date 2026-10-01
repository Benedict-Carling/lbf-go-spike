package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDatasetFilesLayoutAndExclusions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "run1")
	for _, p := range []string{"a.txt", "sub/b.txt", ".DS_Store", "sub/._b.txt"} {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if err := os.WriteFile(path, []byte("PATH='/tmp'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSASEnv(path, "download", "bronze"); err == nil || !strings.Contains(err.Error(), "unexpected key") {
		t.Fatalf("got %v", err)
	}
}

func TestLocalPathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	if _, err := localPath(root, "run1/a.txt"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x", "run1/../../x"} {
		if _, err := localPath(root, bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

func TestRepairWindowsArgs(t *testing.T) {
	got := repairWindowsArgs([]string{"publish", `.\test data" --dry-run  --tag role=x`, "--out", `C:\d"`})
	want := []string{"publish", `.\test data\`, "--dry-run", "--tag", "role=x", "--out", `C:\d\`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestCrateMatchesPythonShape(t *testing.T) {
	files := []localFile{{Rel: "run1/a.txt", Size: 3}}
	tgt := target{Account: "acct", Container: "bronze", User: "u", SubscriptionName: "S", SubscriptionID: "I"}
	raw, err := buildCrate("20260101-x-y-0000", "/data/run1", files, tgt, provenance{}, "", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
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

const minimalProfile = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.org/profiles/minimal-silver",
  "type": "object",
  "required": ["derived_from", "properties", "files"],
  "properties": {
    "derived_from": {"type": "string"},
    "properties": {"type": "object", "required": ["sample_id"]},
    "files": {"type": "array", "minItems": 1}
  }
}`

const extendedProfile = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.org/profiles/extended-silver",
  "$ref": "https://example.org/profiles/minimal-silver",
  "properties": {
    "properties": {"required": ["experiment_type"]},
    "instruments": {"contains": {"properties": {"name": {"const": "CellProfiler"}}, "required": ["name"]}}
  }
}`

func TestProfileValidationWithRefToSibling(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "minimal-silver", "profile.json"), minimalProfile)
	writeFile(t, filepath.Join(root, "extended-silver", "profile.json"), extendedProfile)

	prof, err := loadProfile(filepath.Join(root, "extended-silver"))
	if err != nil {
		t.Fatal(err)
	}
	if prof.ID != "https://example.org/profiles/extended-silver" {
		t.Fatalf("id %s", prof.ID)
	}
	files := []localFile{{Rel: "run1/a.txt", Size: 1}}

	good := provenance{
		DerivedFrom: "20260101-x-y-0000",
		Instruments: []instrument{{"CellProfiler", "4.2.6", "https://cellprofiler.org"}},
		Properties:  map[string]string{"sample_id": "S1", "experiment_type": "paint"},
	}
	if err := prof.validate(profileView("id", good, files)); err != nil {
		t.Fatal(err)
	}

	missingParent := good
	missingParent.Properties = map[string]string{"experiment_type": "paint"}
	err = prof.validate(profileView("id", missingParent, files))
	if err == nil || !strings.Contains(err.Error(), "sample_id") {
		t.Fatalf("parent profile's requirement not enforced: %v", err)
	}

	wrongTool := good
	wrongTool.Instruments = []instrument{{"Fiji", "2", "https://fiji.sc"}}
	if err := prof.validate(profileView("id", wrongTool, files)); err == nil {
		t.Fatal("instrument rule not enforced")
	}
}

func TestProvenanceRules(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"unknown field":         `{"properties": {}, "instrumentz": []}`,
		"parent without tools":  `{"derived_from": "20260101-x-y-0000"}`,
		"tools without parent":  `{"instruments": [{"name": "a", "version": "1", "url": "u"}]}`,
		"incomplete instrument": `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a"}]}`,
		"bad parent id":         `{"derived_from": "../x", "instruments": [{"name": "a", "version": "1", "url": "https://a"}], "properties": {}}`,
		"reserved property":     `{"properties": {"source_path": "/elsewhere"}}`,
		"non-string property":   `{"properties": {"n": 1}}`,
		"url not a uri":         `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "not a url"}], "properties": {}}`,
	}
	for name, body := range cases {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
		writeFile(t, path, body)
		_, err := readProvenance(path)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		t.Logf("%s:\n%v", name, err)
	}
}

func TestCrateRecordsDerivation(t *testing.T) {
	prov := provenance{
		DerivedFrom: "20260101-x-y-0000",
		Instruments: []instrument{{"pipe", "1.0", "https://example.org/pipe"}},
		Properties:  map[string]string{"sample_id": "S1"},
	}
	tgt := target{Account: "acct", Container: "silver", User: "u"}
	raw, err := buildCrate("20260102-a-b-1111", "/data/out", []localFile{{Rel: "out/x", Size: 1}}, tgt, prov, "https://example.org/p", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		`"wasDerivedFrom": {`, `"#source-20260101-x-y-0000"`, `"CreateAction"`,
		`"https://w3id.org/ro/wfrun/process/0.5"`, `"https://example.org/p"`, `"name": "sample_id"`,
		`"description": "Dataset 20260102-a-b-1111"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("crate missing %s", want)
		}
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
	files := []localFile{{Rel: "a", Size: 1}}
	if err := prof.validate(profileView(newID(), prov, files)); err != nil {
		t.Fatal(err)
	}
	if err := prof.validate(profileView(newID(), prov, nil)); err == nil {
		t.Fatal("bronze accepted a dataset with no files")
	}
	if err := prof.validate(profileView("not-an-id", prov, files)); err == nil {
		t.Fatal("bronze accepted a malformed identifier")
	}
}

func TestProfileCanRefBuiltInBronze(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "my", "profile.json"), `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.org/my",
  "$ref": "https://github.com/ImperialCollegeLondon/lbf-data-tools/tree/main/profiles/bronze/0.2.0",
  "properties": {"properties": {"required": ["sample_id"]}}
}`)
	prof, err := loadProfile(filepath.Join(root, "my"))
	if err != nil {
		t.Fatal(err)
	}
	prov := provenance{Properties: map[string]string{"sample_id": "S1"}}
	if err := prof.validate(profileView(newID(), prov, nil)); err == nil || !strings.Contains(err.Error(), "minItems") && !strings.Contains(err.Error(), "files") {
		t.Fatalf("bronze rules not inherited: %v", err)
	}
}
