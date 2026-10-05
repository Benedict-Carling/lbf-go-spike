package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

func TestDatasetFilesLayoutAndExclusions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "run1")
	for _, p := range []string{"a.txt", "sub/b.txt", ".DS_Store", "sub/._b.txt"} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(p)), "x")
	}

	_, files, err := datasetFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := rels(files); got != "run1/a.txt,run1/sub/b.txt" {
		t.Fatalf("got %s", got)
	}

	_, single, err := datasetFiles(filepath.Join(root, "a.txt"))
	if err != nil || len(single) != 1 || single[0].Rel != "a.txt" {
		t.Fatalf("single file: %v %v", single, err)
	}
}

func TestDatasetFilesFollowsSymlinks(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "elsewhere", "r.txt"), "r")
	writeFile(t, filepath.Join(base, "elsewhere", "dir", "d.txt"), "d")
	root := filepath.Join(base, "run1")
	writeFile(t, filepath.Join(root, "own.txt"), "o")
	if os.Symlink(filepath.Join(base, "elsewhere", "r.txt"), filepath.Join(root, "file-link")) != nil {
		t.Skip("cannot create symlinks here")
	}
	must(t, os.Symlink(filepath.Join(base, "elsewhere", "dir"), filepath.Join(root, "dir-link")))

	_, files, err := datasetFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := rels(files); got != "run1/dir-link/d.txt,run1/file-link,run1/own.txt" {
		t.Fatalf("got %s", got)
	}

	must(t, os.Symlink(filepath.Join(base, "gone"), filepath.Join(root, "dangling")))
	must(t, os.Symlink(root, filepath.Join(root, "dir-link", "loop")))
	_, _, err = datasetFiles(root)
	for _, want := range []string{"run1/dangling: broken symlink", "run1/dir-link/loop: symlink loop"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
}

func TestDatasetFilesRefusesWhatCannotRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "run1")
	names := []string{"ok.txt", "12:30.csv", "nul.txt", "trailing."}
	if runtime.GOOS != "windows" {
		names = append(names, `back\slash.txt`)
	}
	for _, n := range names {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644); err != nil {
			t.Skipf("this filesystem cannot hold %q", n)
		}
	}
	_, _, err := datasetFiles(root)
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"nothing was uploaded", "run1/12:30.csv: contains", "run1/nul.txt: \"nul.txt\" is a reserved", "run1/trailing.: "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "ok.txt") {
		t.Errorf("flagged ok.txt:\n%v", err)
	}

	ok := filepath.Join(root, "ok.txt")
	folded := checkFiles([]localFile{{Path: ok, Rel: "run1/Same.txt"}, {Path: ok, Rel: "run1/same.txt"}})
	if len(folded) != 1 || !strings.Contains(folded[0], "run1/Same.txt and run1/same.txt differ only in case") {
		t.Errorf("case collision: %v", folded)
	}

	crateFile := filepath.Join(t.TempDir(), crateName)
	writeFile(t, crateFile, "{}")
	if _, _, err := datasetFiles(crateFile); err == nil || !strings.Contains(err.Error(), "replaced by the crate") {
		t.Errorf("single %s: %v", crateName, err)
	}
}

func TestDatasetFilesRefusesUnreadableFiles(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions do not stop this user reading")
	}
	root := filepath.Join(t.TempDir(), "run1")
	writeFile(t, filepath.Join(root, "a.txt"), "x")
	must(t, os.Chmod(filepath.Join(root, "a.txt"), 0))
	if _, _, err := datasetFiles(root); err == nil || !strings.Contains(err.Error(), "run1/a.txt: cannot be read (permission denied)") {
		t.Fatalf("got %v", err)
	}
}

func TestCrateRecordsWhatFetchVerifies(t *testing.T) {
	files := []localFile{{Rel: "run1/a b#1%.csv", Size: 5, SHA256: "abc"}, {Rel: "run1/old.txt", Size: 3}}
	raw, err := buildCrate("20260101-x-y-0000", "/data/run1", files, target{Container: "bronze"}, provenance{}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := crateFiles(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []crateFile{{"run1/a b#1%.csv", 5, "abc"}, {"run1/old.txt", 3, ""}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}

	problems := compareStored(want, map[string]int64{"run1/a b#1%.csv": 4, crateName: 9, "run1/extra": 1})
	if got := strings.Join(problems, "\n"); got != "run1/a b#1%.csv: stored as 4 bytes, but the crate records 5\n"+
		"run1/old.txt: listed in the crate but not stored\nrun1/extra: stored but not listed in the crate" {
		t.Fatalf("got:\n%s", got)
	}
}

func TestVerifyDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "run1", "a.txt"), "hello")
	writeFile(t, filepath.Join(root, crateName), "crate")
	sum := sha256.Sum256([]byte("hello"))
	files := []crateFile{{"run1/a.txt", 5, hex.EncodeToString(sum[:])}}

	if problem, err := verifyDir(root, files, []byte("crate")); problem != "" || err != nil {
		t.Fatalf("intact copy: %q %v", problem, err)
	}
	writeFile(t, filepath.Join(root, "run1", "a.txt"), "jello")
	if problem, _ := verifyDir(root, files, []byte("crate")); problem != "run1/a.txt has the wrong sha256" {
		t.Fatalf("changed content: %q", problem)
	}
	writeFile(t, filepath.Join(root, "run1", "a.txt"), "hello")
	writeFile(t, filepath.Join(root, "stray.txt"), "x")
	if problem, _ := verifyDir(root, files, []byte("crate")); problem != "stray.txt is not part of the dataset" {
		t.Fatalf("stray file: %q", problem)
	}
}

func TestLocalNameProblems(t *testing.T) {
	files := []crateFile{{Rel: "run1/A.txt"}, {Rel: "run1/a.txt"}, {Rel: "run1/x:y"}}
	if got := localNameProblems(files, "linux"); len(got) != 0 {
		t.Errorf("linux: %v", got)
	}
	if got := localNameProblems(files, "darwin"); len(got) != 1 {
		t.Errorf("darwin: %v", got)
	}
	if got := localNameProblems(files, "windows"); len(got) != 2 {
		t.Errorf("windows: %v", got)
	}
}

func rels(files []localFile) string {
	var out []string
	for _, f := range files {
		out = append(out, f.Rel)
	}
	return strings.Join(out, ",")
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
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

func TestCommandsRejectOtherCommandsFlags(t *testing.T) {
	for _, args := range [][]string{
		{"fetch", "20260101-x-y-0000", "--dry-run"},
		{"fetch", "20260101-x-y-0000", "--provenance", "p.json"},
		{"mint-sas", "--mode", "upload", "--profile", "p"},
		{"logout", "--tag", "tag=x"},
	} {
		if _, _, err := parseArgs(args[0], args[1:]); err == nil || !strings.Contains(err.Error(), "not defined") {
			t.Errorf("%v: got %v", args, err)
		}
	}
	if _, _, err := parseArgs("publish", []string{"data", "--dry-run", "--profile", "p"}); err != nil {
		t.Errorf("publish flags: %v", err)
	}
	if _, _, err := parseArgs("publish", []string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("publish -h: %v", err)
	}
}

func TestCommandHelpListsOwnFlagsInOrderWithDefaults(t *testing.T) {
	var o options
	c, err := newCommand("fetch", &o)
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	c.usage(&buf)
	got := buf.String()
	want := []string{"Required:", "<id>", "Optional:", "--out DIR", "(default .)", "--container NAME", "(default bronze)", "--tag KEY=VALUE", "(default tag=storage)", "--account NAME", "--tenant ID", "Imperial College London", "--sas-env FILE"}
	rest := got
	for _, w := range want {
		i := strings.Index(rest, w)
		if i < 0 {
			t.Fatalf("missing or out of order %q in:\n%s", w, got)
		}
		rest = rest[i+len(w):]
	}
	if strings.Contains(got, "--dry-run") || strings.Contains(got, imperialTenant) {
		t.Errorf("unexpected content:\n%s", got)
	}
}

func TestMissingRequiredArgumentsNamedWithUsage(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"publish"}, "missing <path>"},
		{[]string{"fetch", "--out", "x"}, "missing <id>"},
		{[]string{"mint-sas"}, "missing --mode"},
		{[]string{"logout", "extra"}, `unexpected argument "extra"`},
		{[]string{"fetch", "a", "b"}, `unexpected argument "b"`},
	} {
		_, _, err := parseArgs(tc.args[0], tc.args[1:])
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) || !strings.Contains(err.Error(), "Usage: lbf") {
			t.Errorf("%v: got %v", tc.args, err)
		}
	}
}

func TestTransferStopsStartingAfterAFailure(t *testing.T) {
	var calls atomic.Int64
	jobs := make([]job, 100)
	for i := range jobs {
		jobs[i].run = func(context.Context, func(int64)) error {
			calls.Add(1)
			return errors.New("boom")
		}
	}
	err := transferAll(t.Context(), newProgress(io.Discard, len(jobs), 0), jobs)
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
		_, err := preparePublication(data, provenanceFlags{file: path}, "")
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

func TestProvenanceFlags(t *testing.T) {
	dir := t.TempDir()
	props := filepath.Join(dir, "metadata.json")
	writeFile(t, props, `{"sample_id": "SAM-0001"}`)
	in, err := parseInstrument("name=nf,version=0.1.0,url=https://example.org/a,b")
	if err != nil {
		t.Fatal(err)
	}
	p, err := provenanceFlags{derivedFrom: "20260101-x-y-0000", instruments: []instrument{in}, properties: props}.load()
	if err != nil {
		t.Fatal(err)
	}
	want := instrument{"nf", "0.1.0", "https://example.org/a,b"}
	if p.DerivedFrom != "20260101-x-y-0000" || len(p.Instruments) != 1 || p.Instruments[0] != want || p.Properties["sample_id"] != "SAM-0001" {
		t.Fatalf("%+v", p)
	}

	nonString := filepath.Join(dir, "n.json")
	writeFile(t, nonString, `{"n": 1}`)
	null := filepath.Join(dir, "null.json")
	writeFile(t, null, `null`)
	for name, f := range map[string]provenanceFlags{
		"parent without tools":  {derivedFrom: "20260101-x-y-0000"},
		"tools without parent":  {instruments: []instrument{want}},
		"incomplete instrument": {derivedFrom: "20260101-x-y-0000", instruments: []instrument{{Name: "nf"}}},
		"non-string property":   {properties: nonString},
		"null properties":       {properties: null},
		"with --provenance":     {file: props, derivedFrom: "20260101-x-y-0000"},
	} {
		if _, err := f.load(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, bad := range []string{"nf", "colour=red,name=nf"} {
		if _, err := parseInstrument(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
	if _, _, err := parseArgs("publish", []string{"data", "--instrument", "nf"}); err == nil {
		t.Error("malformed --instrument accepted by publish")
	}
}

func TestJSONFlagOnPublishAndFetchOnly(t *testing.T) {
	for _, args := range [][]string{{"publish", "data", "--json"}, {"fetch", "20260101-x-y-0000", "--json"}} {
		if o, _, err := parseArgs(args[0], args[1:]); err != nil || !o.json {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, _, err := parseArgs("mint-sas", []string{"--mode", "upload", "--json"}); err == nil {
		t.Error("mint-sas accepted --json")
	}
}

func TestDataDir(t *testing.T) {
	root := filepath.Join("out", "id")
	for _, tc := range []struct {
		rels []string
		want string
	}{
		{[]string{"plate1/a.nd2", "plate1/sub/b.csv"}, filepath.Join(root, "plate1")},
		{[]string{"plate1/a.nd2", "plate2/b.nd2"}, root},
		{[]string{"plate1/a.nd2", "loose.txt"}, root},
		{nil, root},
	} {
		files := make([]crateFile, len(tc.rels))
		for i, r := range tc.rels {
			files[i].Rel = r
		}
		if got := dataDir(root, files); got != tc.want {
			t.Errorf("%v: got %s, want %s", tc.rels, got, tc.want)
		}
	}
}

func TestVersionedProfilesResolveSiblings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "minimal-silver", "0.1.0", "profile.json"), minimalSilverProfile)
	writeFile(t, filepath.Join(root, "cell-painting", "0.1.0", "profile.json"), cellPaintingProfile)
	prof, err := loadProfile(filepath.Join(root, "cell-painting", "0.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	if got := prof.ids(); len(got) != 3 {
		t.Fatalf("chain %v", got)
	}
}

func TestUnrelatedProfileJSONNearbyIsIgnored(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app", "cfg", "profile.json"), `{"theme": "dark"}`)
	writeFile(t, filepath.Join(root, "work", "notes", "profile.json"), `not json`)
	writeFile(t, filepath.Join(root, "work", "myprof", "profile.json"), minimalSilverProfile)
	if _, err := loadProfile(filepath.Join(root, "work", "myprof")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "work", "bad", "profile.json"), `{"theme": "dark"}`)
	if _, err := loadProfile(filepath.Join(root, "work", "bad")); err == nil {
		t.Fatal("a profile without $id was accepted")
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

func TestChooseAccount(t *testing.T) {
	m := func(name, sub string) accountMatch {
		return accountMatch{Name: name, Location: "uksouth", Sub: &armsubscriptions.Subscription{DisplayName: new(sub)}}
	}
	two := []accountMatch{m("lbfb", "Sub B"), m("lbfa", "Sub A")}
	never := func(string, []string) (int, error) { t.Fatal("picker called"); return 0, nil }

	if got, err := chooseAccount([]accountMatch{m("lbfa", "Sub A")}, "tag=x", "", never); err != nil || got.Name != "lbfa" {
		t.Errorf("single match: %v %v", got.Name, err)
	}
	if got, err := chooseAccount(two, "tag=x", "lbfb", never); err != nil || got.Name != "lbfb" {
		t.Errorf("--account: %v %v", got.Name, err)
	}
	if _, err := chooseAccount(two, "tag=x", "other", never); err == nil || !strings.Contains(err.Error(), `"other"`) || !strings.Contains(err.Error(), "--account lbfa") {
		t.Errorf("unknown --account: %v", err)
	}
	if _, err := chooseAccount(nil, "tag=x", "", never); err == nil || !strings.Contains(err.Error(), "no storage account tagged") {
		t.Errorf("no match: %v", err)
	}
	_, err := chooseAccount(two, "tag=x", "", nil)
	if err == nil || !strings.Contains(err.Error(), "--account lbfa") || !strings.Contains(err.Error(), "--account lbfb") || !strings.Contains(err.Error(), "Sub B") {
		t.Errorf("no terminal: %v", err)
	}

	var labels []string
	got, err := chooseAccount(two, "tag=x", "", func(_ string, l []string) (int, error) { labels = l; return 1, nil })
	if err != nil || got.Name != "lbfb" {
		t.Errorf("picked: %v %v", got.Name, err)
	}
	if len(labels) != 2 || !strings.HasPrefix(labels[0], "lbfa") || !strings.Contains(labels[1], "Sub B") || !strings.Contains(labels[1], "uksouth") {
		t.Errorf("labels not sorted or incomplete: %q", labels)
	}
	if _, err := chooseAccount(two, "tag=x", "", func(string, []string) (int, error) { return 0, errNoChoice }); !errors.Is(err, errNoChoice) {
		t.Errorf("cancelled: %v", err)
	}
}

func TestPickArrows(t *testing.T) {
	items := []string{"a", "b", "c"}
	for _, tc := range []struct {
		keys string
		want int
	}{
		{"\r", 0},
		{"\x1b[B\r", 1},
		{"\x1b[B\x1b[B\x1b[B\x1b[B\n", 2},
		{"\x1b[B\x1b[A\x1b[A\r", 0},
		{"jjk\r", 1},
		{"\x1bOB\r", 1},
		{"x\r", 0},
	} {
		got, err := pickArrows(strings.NewReader(tc.keys), io.Discard, "Choose:", items)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %d %v, want %d", tc.keys, got, err, tc.want)
		}
	}
	for _, keys := range []string{"\x03", "q", "\x1b", "", "\x1b[B"} {
		if _, err := pickArrows(strings.NewReader(keys), io.Discard, "Choose:", items); !errors.Is(err, errNoChoice) {
			t.Errorf("%q: got %v, want cancel", keys, err)
		}
	}
	var out strings.Builder
	got, err := pickArrows(io.MultiReader(strings.NewReader("\x1b["), strings.NewReader("B"), strings.NewReader("\r")), &out, "Choose:", items)
	if err != nil || got != 1 {
		t.Errorf("keys split across reads: got %d %v", got, err)
	}
	if s := out.String(); !strings.HasPrefix(s, "Choose:") || !strings.Contains(s, "(*) a") || !strings.Contains(s, "\x1b[3A") || !strings.Contains(s[strings.LastIndex(s, "\x1b[3A"):], "(*) b") {
		t.Errorf("render:\n%q", out.String())
	}
}

func TestPickNumbered(t *testing.T) {
	var out strings.Builder
	got, err := pickNumbered(strings.NewReader("x\n9\n\n 2 \r\n"), &out, "Choose:", []string{"a", "b"})
	if err != nil || got != 1 {
		t.Errorf("got %d %v", got, err)
	}
	if !strings.Contains(out.String(), "1) a") || strings.Count(out.String(), "Choose [1-2]: ") != 4 {
		t.Errorf("render:\n%s", out.String())
	}
	if _, err := pickNumbered(strings.NewReader("9\n"), io.Discard, "Choose:", []string{"a", "b"}); !errors.Is(err, errNoChoice) {
		t.Errorf("eof: %v", err)
	}
}

func TestProgressLogsEachFinishedFileWhenNotATerminal(t *testing.T) {
	var out strings.Builder
	ok := []job{{name: "run1/a.txt", size: 2048, run: func(_ context.Context, p func(int64)) error { p(2048); return nil }}}
	if err := transferAll(t.Context(), newProgress(&out, 1, 2048), ok); err != nil {
		t.Fatal(err)
	}
	failed := []job{{name: "run1/b.txt", size: 10, run: func(context.Context, func(int64)) error { return errors.New("boom") }}}
	if err := transferAll(t.Context(), newProgress(&out, 1, 10), failed); err == nil {
		t.Fatal("failure not reported")
	}
	if got, want := out.String(), "  [1/1] run1/a.txt (2.0 KiB)\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLiveProgressFitsAnyTerminal(t *testing.T) {
	escape := regexp.MustCompile(`\x1b\[\d*[A-Za-z]\r?`)
	names := []string{"a.txt", "run1/细胞图像/样本一.tiff", "tab\there\nnewline.bin", "\u202eevil.exe",
		"🧪🧫/" + strings.Repeat("x", 300) + ".parquet", "é́́.csv", "plate1/a.parquet", "b", "c", "d"}
	for _, height := range []int{1, 2, 3, 5, 8, 24, 60} {
		for width := 1; width <= 200; width++ {
			now := time.Now()
			p := &progress{live: true, size: func() (int, int) { return width, height }, files: 40, total: 40 << 30,
				samples: []sample{{now.Add(-time.Minute), 0}}, done: 7, settled: 9 << 30}
			for i, name := range names {
				b := p.start(name, int64(i+1)<<30)
				b.set(int64(i) << 29)
			}
			for frame := range 2 {
				lines := strings.Split(escape.ReplaceAllString(p.render(now, false), ""), "\n")
				lines = lines[:len(lines)-1]
				if len(lines) > max(height-2, 1) {
					t.Fatalf("%dx%d frame %d: %d lines", width, height, frame, len(lines))
				}
				for _, l := range lines {
					if len(l) > max(width-1, 0) || strings.ContainsAny(l, "\t\r\u202e") {
						t.Fatalf("%dx%d frame %d: %q", width, height, frame, l)
					}
				}
			}
		}
	}
}

func TestLiveProgressRewindsRewrappedFrame(t *testing.T) {
	width := 120
	p := &progress{live: true, size: func() (int, int) { return width, 24 }, files: 1, total: 100, samples: []sample{{time.Now(), 0}}}
	p.start("a.txt", 100)
	p.render(time.Now(), false)
	want := 0
	for _, w := range p.frame {
		want += (w + 39) / 40
	}
	width = 40
	if got := p.render(time.Now(), false); !strings.HasPrefix(got, fmt.Sprintf("\x1b[%dA", want)) || want <= len(p.frame) {
		t.Fatalf("want rewind of %d rows, got %q", want, got)
	}
}

func TestProgressFormatting(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{bar(0, 100, 8), "[>       ]"},
		{bar(50, 100, 8), "[====>   ]"},
		{bar(100, 100, 8), "[========]"},
		{bar(0, 0, 8), "[========]"},
		{clipLeft("a.txt", 8), "a.txt"},
		{clipLeft("run1/images/plate1.tiff", 12), "...ate1.tiff"},
		{clipLeft("run1/细胞.tiff", 12), "...胞.tiff"},
		{clipLeft("run1/a.tiff", 2), ".."},
		{printable("bad\tname\n\u202egpj.exe"), "bad?name??gpj.exe"},
		{fit("ab细胞", 4), "ab"},
		{fit("ab细胞", 5), "ab细"},
		{fit("abc", -1), ""},
		{shortDuration(42 * time.Second), "42s"},
		{shortDuration(5*time.Minute + 3*time.Second), "5m03s"},
		{shortDuration(2*time.Hour + 7*time.Minute), "2h07m"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.1.6", "v0.1.5", true},
		{"v0.2.0", "v0.1.10", true},
		{"v0.1.10", "v0.1.9", true},
		{"v0.1.5", "v0.1.5", false},
		{"v0.1.4", "v0.1.5", false},
		{"v0.1.6", "dev", false},
		{"", "v0.1.5", false},
	} {
		if got := newerVersion(c.latest, c.current); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}

func fakeReleases(t *testing.T, tag string, bin []byte, sum string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
		case "/releases/download/" + tag + "/SHA256SUMS":
			fmt.Fprintf(w, "%s  lbf-other-os\n%s  %s\n", strings.Repeat("0", 64), sum, assetName())
		case "/releases/download/" + tag + "/" + assetName():
			w.Write(bin)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpgradeReplacesExecutable(t *testing.T) {
	bin := []byte("new lbf")
	sum := sha256.Sum256(bin)
	srv := fakeReleases(t, "v0.2.0", bin, hex.EncodeToString(sum[:]))
	exe := filepath.Join(t.TempDir(), "lbf")
	writeFile(t, exe, "old lbf")

	latest, upgraded, err := upgrade(context.Background(), srv.URL+"/releases", "v0.1.5", exe)
	if err != nil || !upgraded || latest != "v0.2.0" {
		t.Fatalf("got %q %v %v", latest, upgraded, err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new lbf" {
		t.Fatalf("exe holds %q", got)
	}

	_, upgraded, err = upgrade(context.Background(), srv.URL+"/releases", "v0.2.0", exe)
	if err != nil || upgraded {
		t.Fatalf("up to date: upgraded=%v err=%v", upgraded, err)
	}
}

func TestUpgradeRejectsChecksumMismatch(t *testing.T) {
	srv := fakeReleases(t, "v0.2.0", []byte("tampered"), strings.Repeat("a", 64))
	exe := filepath.Join(t.TempDir(), "lbf")
	writeFile(t, exe, "old lbf")

	if _, _, err := upgrade(context.Background(), srv.URL+"/releases", "v0.1.5", exe); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want checksum error, got %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old lbf" {
		t.Fatalf("exe changed to %q", got)
	}
}

func TestLatestVersionIsCached(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/releases/tag/v0.3.0", http.StatusFound)
	}))
	defer srv.Close()
	cache := filepath.Join(t.TempDir(), "lbf", "latest-version")

	for range 2 {
		if got := cachedLatestVersion(context.Background(), srv.URL+"/releases", cache); got != "v0.3.0" {
			t.Fatalf("got %q", got)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("asked GitHub %d times, want 1", hits.Load())
	}

	stale := time.Now().Add(-2 * updateCheckEvery)
	os.Chtimes(cache, stale, stale)
	srv.Close()
	if got := cachedLatestVersion(context.Background(), srv.URL+"/releases", cache); got != "v0.3.0" {
		t.Fatalf("offline refresh lost the cached version: %q", got)
	}
}

func TestUpgradeWarningNamesBothVersionsAndCommand(t *testing.T) {
	w := upgradeWarning("v0.1.5", "v0.2.0")
	for _, s := range []string{"v0.1.5", "v0.2.0", "lbf upgrade"} {
		if !strings.Contains(w, s) {
			t.Errorf("warning lacks %q:\n%s", s, w)
		}
	}
}

func TestUpdateCheckGivesUpOnASilentServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	start := time.Now()
	got := cachedLatestVersion(context.Background(), srv.URL+"/releases", filepath.Join(t.TempDir(), "latest-version"))
	if took := time.Since(start); got != "" || took > updateCheckLimit+time.Second {
		t.Fatalf("got %q after %v", got, took)
	}
}

func TestUpgradeIntoReadOnlyFolderSaysWhatToDo(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs Unix permissions")
	}
	bin := []byte("new lbf")
	sum := sha256.Sum256(bin)
	srv := fakeReleases(t, "v0.2.0", bin, hex.EncodeToString(sum[:]))
	dir := t.TempDir()
	exe := filepath.Join(dir, "lbf")
	writeFile(t, exe, "old lbf")
	os.Chmod(dir, 0o555)
	defer os.Chmod(dir, 0o755)

	_, _, err := upgrade(context.Background(), srv.URL+"/releases", "v0.1.5", exe)
	if err == nil || !strings.Contains(err.Error(), "README") {
		t.Fatalf("got %v", err)
	}
}
