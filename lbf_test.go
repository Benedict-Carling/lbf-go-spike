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
		Account: "lbfdatastorehnstest", Container: "bronze", SAS: "sv=x&sig=y",
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

func TestCrateMatchesPythonShape(t *testing.T) {
	files := []localFile{{Rel: "run1/a.txt", Size: 3}}
	tgt := target{Account: "acct", Container: "bronze", User: "u", SubscriptionName: "S", SubscriptionID: "I"}
	raw, err := buildCrate("20260101-x-y-0000", "/data/run1", files, tgt, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
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
