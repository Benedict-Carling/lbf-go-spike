package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/ImperialCollegeLondon/lbf-cli/internal/psbuild"
)

var psVersionedScripts sync.Map

// lbf.ps1 built as version, in a folder of its own.
func psScriptVersion(t *testing.T, version string) string {
	t.Helper()
	if p, ok := psVersionedScripts.Load(version); ok {
		return p.(string)
	}
	script, err := psbuild.Build("../..", version)
	must(t, err)
	dir, err := os.MkdirTemp("", "lbf-ps-"+version+"-")
	must(t, err)
	path := filepath.Join(dir, "lbf.ps1")
	must(t, os.WriteFile(path, script, 0o644))
	p, _ := psVersionedScripts.LoadOrStore(version, path)
	return p.(string)
}

func psEnviron(env map[string]string) []string {
	out := slices.Clone(os.Environ())
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// Dot-sources lbf.ps1 without running it, then runs body, which prints what the test compares.
func psCall(t *testing.T, env map[string]string, body string) lbfResult {
	t.Helper()
	ps := powershell(t)
	script := psScriptVersion(t, "dev")
	command := fmt.Sprintf("$env:LBF_TEST_NO_MAIN = '1'; . '%s'; Remove-Item Env:LBF_TEST_NO_MAIN\n%s", strings.ReplaceAll(script, "'", "''"), body)
	cmd := psCommand(ps, command)
	cmd.Env = psEnviron(env)
	r := runCmd(t, cmd)
	r.stdout = strings.ReplaceAll(r.stdout, "\r\n", "\n")
	r.stderr = strings.ReplaceAll(r.stderr, "\r\n", "\n")
	if r.code != 0 {
		t.Fatalf("PowerShell exited %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	return r
}

// Runs script as a user does at a PowerShell prompt.
func psRunScript(t *testing.T, script string, env map[string]string, args ...string) lbfResult {
	t.Helper()
	ps := powershell(t)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + psSingleQuotes.Replace(a) + "'"
	}
	command := fmt.Sprintf("& '%s' %s; exit $LASTEXITCODE", strings.ReplaceAll(script, "'", "''"), strings.Join(quoted, " "))
	cmd := psCommand(ps, command)
	cmd.Env = psEnviron(env)
	r := runCmd(t, cmd)
	r.stdout = strings.ReplaceAll(r.stdout, "\r\n", "\n")
	r.stderr = strings.ReplaceAll(r.stderr, "\r\n", "\n")
	return r
}

func psQuote(s string) string { return "'" + psSingleQuotes.Replace(s) + "'" }

// Each crate's flattenCrate result as JSON, or ERR and the error.
func goFlattened(raw []byte) string {
	row, files, names, err := flattenCrate(raw)
	if err != nil {
		return "ERR " + err.Error()
	}
	b, err := json.Marshal(map[string]any{"row": row, "files": files, "names": names})
	if err != nil {
		return "ERR marshal: " + err.Error()
	}
	return string(b)
}

func browseCrates(t *testing.T) map[string][]byte {
	t.Helper()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	crates := map[string][]byte{}
	build := func(name string, p publication, tg target) {
		raw, err := p.buildCrate(tg, at)
		must(t, err)
		crates[name] = raw
	}
	build("derived", publication{ID: "20260102-a-b-1234", Source: "/data/run1",
		Files: []localFile{{Rel: "run1/a.csv", Size: 3, SHA256: "abc"}, {Rel: "run1/b.csv", Size: 4}},
		Provenance: provenance{DerivedFrom: "20260101-x-y-0000", Instruments: []instrument{{"CellProfiler", "4.2.6", "https://cellprofiler.org"}, {"pipe", "0.1.0", "https://example.org/pipe/0.1.0"}},
			Properties: map[string]string{"sample_id": "S1", "plate": "P 1"}}}, target{User: "ann@example.org"})
	build("plain", publication{ID: "20260102-c-d-5678", Source: "/data/run2", Files: []localFile{{Rel: "run2/x", Size: 1}},
		Provenance: provenance{Properties: map[string]string{}}}, target{})
	build("profiled", publication{ID: "20260102-e-f-9abc", Source: "/data/run3", Files: []localFile{{Rel: "run3/a", Size: 1}, {Rel: "ro-crate-profiles/p1/0.1.0/profile.json", Size: 2, Content: []byte("{}")}},
		Provenance: provenance{Properties: map[string]string{}}, Profile: &profile{rules: []rule{{id: "https://example.org/p1"}, {id: "https://example.org/p2"}}}}, target{})

	var doc map[string]any
	must(t, json.Unmarshal(crates["plain"], &doc))
	graph := doc["@graph"].([]any)
	for _, e := range graph {
		if e.(map[string]any)["@id"] == "./" {
			e.(map[string]any)["keywords"] = "screen"
			e.(map[string]any)["isPartOf"] = map[string]any{"@id": "#cycle-3"}
		}
	}
	doc["@graph"] = append(graph, map[string]any{"@id": "#cycle-3", "@type": "Collection", "name": "Cycle 3"})
	raw, err := json.Marshal(doc)
	must(t, err)
	crates["taught nothing"] = raw

	for name, s := range map[string]string{
		"iri links": `{"@graph": [
			{"@id": "ro-crate-metadata.json", "about": {"@id": "./"}},
			{"@id": "./", "@type": "Dataset", "conformsTo": {"@id": "https://w3id.org/ro/wfrun/process/0.5"}, "author": {"@id": "mailto:a@example.org"}},
			{"@id": "https://w3id.org/ro/wfrun/process/0.5", "name": "Process Run Crate", "version": "0.5"},
			{"@id": "mailto:a@example.org", "@type": "Person", "name": "Ann"}]}`,
		"not json":         `{`,
		"empty":            ``,
		"trailing":         `{"@graph": []} x`,
		"bad escape":       `{"@graph": ["\q"]}`,
		"array":            `[]`,
		"string":           `"x"`,
		"null":             `null`,
		"no graph":         `{}`,
		"graph object":     `{"@graph": {}}`,
		"graph of strings": `{"@graph": ["x", 1]}`,
		"graph of nulls":   `{"@graph": [null, {"@id": "./", "name": "n"}]}`,
		"upper graph":      `{"@GRAPH": [{"@id": "./", "name": "upper"}], "@graph": [{"@id": "./", "name": "lower"}]}`,
		"about not string": `{"@graph": [{"@id": "ro-crate-metadata.json", "about": {"@id": 5}}, {"@id": "./", "name": "n"}]}`,
		"about elsewhere":  `{"@graph": [{"@id": "ro-crate-metadata.json", "about": {"@id": "#root"}}, {"@id": "#root", "name": "r"}]}`,
		"about not a map":  `{"@graph": [{"@id": "ro-crate-metadata.json", "about": "#root"}, {"@id": "./", "name": "dot"}]}`,
		"duplicate keys":   `{"@graph": [{"@id": "./", "name": "first", "name": "second"}, {"@id": "./", "name": "again"}]}`,
		"values of every kind": `{"@graph": [{"@id": "./", "n": 12345678, "f": 1.5, "small": 0.00001, "big": 1e21, "t": true, "z": null, "e": [],
			"list": [1, "a", {"@id": "#x"}], "nested": [[1, 2], {"b": 1, "a": [true]}], "obj": {"k": "v"}, "objid": {"@id": "#nothing"},
			"vals": [{"@id": "#v"}, {"@id": "#i"}, {"@id": "#c"}], "@type": ["Dataset", "Thing"]},
			{"@id": "#x", "name": {"m": 1}, "version": 2},
			{"@id": "#v", "value": 7}, {"@id": "#i", "identifier": "ID-1", "value": "no"}, {"@id": "#c", "contentUrl": "https://c"}]}`,
		"properties": `{"@graph": [{"@id": "./", "additionalProperty": [{"@id": "#p1"}, {"name": "inline", "value": 3}, {"name": 5, "value": "x"}, "str", {"@id": "#nope", "name": "unresolved"}, {"@id": "#p2"}]},
			{"@id": "#p1", "name": "sample", "value": "S9"}, {"@id": "#p2", "name": "novalue"}]}`,
		"properties nil key": `{"@graph": [{"@id": "./", "additionalProperty": "str"}, {"@id": "<nil>", "name": "fromnil", "value": "odd"}]}`,
		"runs": `{"@graph": [{"@id": "./", "name": "r"},
			{"@id": "#run1", "@type": "CreateAction", "result": [{"@id": "#other"}, {"@id": "./"}], "instrument": [{"@id": "https://example.org/tool/1.0"}, {"@id": "#t2"}], "agent": {"@id": "mailto:b@example.org"}, "startTime": "2026"},
			{"@id": "#run2", "@type": ["Thing", "CreateAction"], "result": {"@id": "#other"}, "agent": "x"},
			{"@id": "#run3", "@type": "createaction", "result": {"@id": "./"}, "agent": "lower"},
			{"@id": "https://example.org/tool/1.0", "name": "tool", "version": "1.0"},
			{"@id": "#t2", "name": "t2", "version": "9"},
			{"@id": "mailto:b@example.org", "name": "mailto:b@example.org"}]}`,
		"repo iri": `{"@graph": [{"@id": "./", "instrument": [{"@id": "https://github.com/x/y"}, {"@id": "https://example.org/v/2.0"}]},
			{"@id": "https://github.com/x/y", "name": "y", "version": "3.1"}, {"@id": "https://example.org/v/2.0", "name": "v", "version": 2}]}`,
		"iri shapes": `{"@graph": [{"@id": "./", "links": [{"@id": "http://x:abc/"}, {"@id": "a:b"}, {"@id": "1a:b"}, {"@id": ":x"}, {"@id": "http://x/%zz"}, {"@id": "http://x/#%zz"},
			{"@id": "http://u ser@x/"}, {"@id": "http://[::1]:80/"}, {"@id": "http://[::1/"}, {"@id": "http://x y/"}, {"@id": "urn:isbn:1"}, {"@id": "x+y.z-w:rest"}, {"@id": "http://x:8080/"}, {"@id": "ht%74p://x"}]},
			{"@id": "http://x:abc/", "name": "badport"}, {"@id": "a:b", "name": "opaque"}, {"@id": "1a:b", "name": "digit"}, {"@id": ":x", "name": "colon"},
			{"@id": "http://x/%zz", "name": "badesc"}, {"@id": "http://x/#%zz", "name": "badfrag"}, {"@id": "http://u ser@x/", "name": "baduser"},
			{"@id": "http://[::1]:80/", "name": "v6"}, {"@id": "http://[::1/", "name": "v6open"}, {"@id": "http://x y/", "name": "space"}, {"@id": "urn:isbn:1", "name": "urn"},
			{"@id": "x+y.z-w:rest", "name": "chars"}, {"@id": "http://x:8080/", "name": "port"}, {"@id": "ht%74p://x", "name": "pct"}]}`,
		"files": `{"@graph": [{"@id": "./", "hasPart": [{"@id": "a"}, {"@id": "b"}, {"@id": "c"}, {"@id": "d"}, {"@id": "e"}, {"@id": "f"}, {"@id": "g"}, {"@id": "h"}, {"@id": "i"}, {"@id": "j"}, {"@id": "k"}, {"@id": "notfile"}, {"@id": "missing"}, "str", {"@id": ""}]},
			{"@id": "a", "@type": "File", "contentSize": "12", "sha256": "aa"}, {"@id": "b", "@type": ["File", "Thing"], "contentSize": 34, "sha256": 5},
			{"@id": "c", "@type": "File", "contentSize": "0x1F"}, {"@id": "d", "@type": "File", "contentSize": "017"}, {"@id": "e", "@type": "File", "contentSize": "12abc"},
			{"@id": "f", "@type": "File", "contentSize": 1.5e7}, {"@id": "g", "@type": "File", "contentSize": "1_000"}, {"@id": "h", "@type": "File", "contentSize": " -5"},
			{"@id": "i", "@type": "File", "contentSize": "9223372036854775808"}, {"@id": "j", "@type": "File"}, {"@id": "k", "@type": "File", "contentSize": "0b101", "sha256": ""},
			{"@id": "notfile", "@type": "Dataset", "contentSize": "9"}, {"@id": "", "@type": "File", "contentSize": "2"}]}`,
		"no files": `{"@graph": [{"@id": "./", "hasPart": []}]}`,
	} {
		crates[name] = []byte(s)
	}
	return crates
}

func TestConformanceBrowseFlattenCrate(t *testing.T) {
	powershell(t)
	crates := browseCrates(t)
	dir := t.TempDir()
	names := slices.Sorted(maps.Keys(crates))
	var paths []string
	for i, name := range names {
		p := filepath.Join(dir, fmt.Sprintf("%02d.json", i))
		must(t, os.WriteFile(p, crates[name], 0o644))
		paths = append(paths, psQuote(p))
	}
	r := psCall(t, nil, `$out = foreach ($p in @(`+strings.Join(paths, ",")+`)) {
		try {
			$f = ConvertFrom-LbfBrowseCrate ($script:Utf8NoBom.GetString([IO.File]::ReadAllBytes($p)))
			ConvertTo-LbfJson @{ row = $f.Row; files = $f.Files; names = $f.Names }
		} catch { 'ERR ' + (Get-LbfErrorText $_) }
	}
	ConvertTo-LbfJson ([object[]]@($out))`)
	var got []string
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil || len(got) != len(names) {
		t.Fatalf("PowerShell printed %q (%v)\nstderr: %s", r.stdout, err, r.stderr)
	}
	for i, name := range names {
		if want := goFlattened(crates[name]); got[i] != want {
			t.Errorf("%s:\n go: %s\n ps: %s", name, want, got[i])
		}
	}
}

func TestConformanceBrowseFetchCommand(t *testing.T) {
	cases := [][4]string{
		{"X", "bronze", "tag=storage", "acc"},
		{"X", "silver", "tag=storage-test", "acc"},
		{"20260102-a-b-1234", "Bronze", "tag=Storage", "a"},
	}
	var calls []string
	for _, c := range cases {
		calls = append(calls, fmt.Sprintf("Get-LbfFetchCommand %s %s %s %s", psQuote(c[0]), psQuote(c[1]), psQuote(c[2]), psQuote(c[3])))
	}
	r := psCall(t, nil, strings.Join(calls, "\n"))
	got := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	for i, c := range cases {
		if want := fetchCommand(c[0], c[1], c[2], c[3]); i >= len(got) || got[i] != want {
			t.Errorf("fetchCommand%v: go %q, ps %q", c, want, got)
		}
	}
}

// A storage account answering the Blob REST calls browse makes, with a bearer token or, for the Go SDK, none.
type fakeAccount struct {
	srv        *httptest.Server
	containers []string
	crates     map[string]map[string][]byte // container -> id -> crate; nil crate means the folder has none
	locked     map[string]bool
	denyList   bool
	crateGets  atomic.Int32
	badAuth    atomic.Int32
}

func newFakeAccount(t *testing.T) *fakeAccount {
	t.Helper()
	f := &fakeAccount{crates: map[string]map[string][]byte{}, locked: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAccount) fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("x-ms-error-code", code)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, "\ufeff<?xml version=\"1.0\" encoding=\"utf-8\"?><Error><Code>%s</Code><Message>%s</Message></Error>", code, msg)
}

func (f *fakeAccount) serve(w http.ResponseWriter, r *http.Request) {
	if a := r.Header.Get("Authorization"); a != "" && a != "Bearer test-token" {
		f.badAuth.Add(1)
		f.fail(w, http.StatusUnauthorized, "InvalidAuthenticationInfo", "bad token")
		return
	}
	q := r.URL.Query()
	page := func(items []string, wrap func(string) string) (string, string) {
		start := 0
		if m := q.Get("marker"); m != "" {
			fmt.Sscan(m, &start)
		}
		end := min(start+2, len(items))
		var b strings.Builder
		for _, it := range items[start:end] {
			b.WriteString(wrap(it))
		}
		next := ""
		if end < len(items) {
			next = fmt.Sprint(end)
		}
		return b.String(), next
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case path == "" && q.Get("comp") == "list":
		if f.denyList {
			f.fail(w, http.StatusForbidden, "AuthorizationPermissionMismatch", "This request is not authorized to perform this operation using this permission.")
			return
		}
		body, next := page(f.containers, func(s string) string { return "<Container><Name>" + s + "</Name><Properties/></Container>" })
		fmt.Fprintf(w, "\ufeff<?xml version=\"1.0\" encoding=\"utf-8\"?><EnumerationResults ServiceEndpoint=\"%s/\"><Containers>%s</Containers><NextMarker>%s</NextMarker></EnumerationResults>", f.srv.URL, body, next)
	case !strings.Contains(path, "/") && q.Get("comp") == "list" && q.Get("restype") == "container":
		if f.locked[path] {
			f.fail(w, http.StatusForbidden, "AuthorizationPermissionMismatch", "This request is not authorized to perform this operation using this permission.")
			return
		}
		ids, ok := f.crates[path]
		if !ok {
			f.fail(w, http.StatusNotFound, "ContainerNotFound", "The specified container does not exist.")
			return
		}
		body, next := page(slices.Sorted(maps.Keys(ids)), func(s string) string { return "<BlobPrefix><Name>" + s + "/</Name></BlobPrefix>" })
		fmt.Fprintf(w, "\ufeff<?xml version=\"1.0\" encoding=\"utf-8\"?><EnumerationResults ServiceEndpoint=\"%s/\" ContainerName=\"%s\"><Delimiter>/</Delimiter><Blobs>%s<Blob><Name>top.txt</Name><Properties/></Blob></Blobs><NextMarker>%s</NextMarker></EnumerationResults>", f.srv.URL, path, body, next)
	case strings.HasSuffix(path, "/"+crateName):
		f.crateGets.Add(1)
		c, rest, _ := strings.Cut(path, "/")
		raw := f.crates[c][strings.TrimSuffix(rest, "/"+crateName)]
		if raw == nil {
			f.fail(w, http.StatusNotFound, "BlobNotFound", "The specified blob does not exist.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	default:
		f.fail(w, http.StatusBadRequest, "InvalidUri", "unexpected "+r.URL.String())
	}
}

func psCacheRoot(home string) string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Local")
	case "darwin":
		return filepath.Join(home, "Library", "Caches")
	}
	return filepath.Join(home, ".cache")
}

func psHomeEnv(home string) map[string]string {
	return map[string]string{
		"HOME": home, "USERPROFILE": home, "LOCALAPPDATA": filepath.Join(home, "AppData", "Local"), "XDG_CACHE_HOME": "",
		"LBF_NO_UPDATE_CHECK": "1", "TERM": "dumb",
	}
}

const psStubSignIn = `
function Get-LbfCredential { param($Tenant) @{ Credential = 'cred'; Source = 'test' } }
function Find-LbfAccounts { param($Credential, $Tag) , @(@{ Name = 'acc'; Location = 'uksouth' }) }
function Select-LbfAccount { param($Matches, $Tag, $Want) @($Matches)[0] }
function Get-LbfAccessToken { param($Credential, $Scope) if ($Scope -ne 'https://storage.azure.com/.default') { throw "scope $Scope" }; 'test-token' }
function Open-LbfBrowser { param($Url) [Console]::Error.WriteLine("opened $Url") }
`

func TestConformanceBrowseSyncCrates(t *testing.T) {
	powershell(t)
	f := newFakeAccount(t)
	f.crates["c"] = map[string][]byte{"done": []byte(`{}`), "partway": nil, "a": []byte(`{"x":1}`), "b": []byte(`[]`), "z": []byte(`"z"`)}
	f.locked["locked"] = true
	ctx := context.Background()
	goCache, psCache := t.TempDir(), t.TempDir()

	cc, err := container.NewClientWithNoCredential(f.srv.URL+"/c", nil)
	must(t, err)
	goIDs, err := syncCrates(ctx, cc, goCache)
	must(t, err)
	lc, err := container.NewClientWithNoCredential(f.srv.URL+"/locked", nil)
	must(t, err)
	_, goLockedErr := syncCrates(ctx, lc, t.TempDir())

	f.crateGets.Store(0)
	r := psCall(t, nil, fmt.Sprintf(`$c = New-LbfHttpClient
		ConvertTo-LbfJson ([object[]](Sync-LbfCrates $c %s 'c' 'test-token' %s))
		try { Sync-LbfCrates $c %[1]s 'locked' 'test-token' %[3]s } catch { 'locked:' + (Test-LbfBlobCode $_ @('AuthorizationPermissionMismatch')) }
		ConvertTo-LbfJson ([object[]](Sync-LbfCrates $c %[1]s 'c' 'test-token' %[2]s))`, psQuote(f.srv.URL), psQuote(psCache), psQuote(t.TempDir())))
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	wantIDs, _ := json.Marshal(goIDs)
	if len(lines) != 3 || lines[0] != string(wantIDs) || lines[2] != string(wantIDs) || lines[1] != "locked:True" {
		t.Fatalf("got %q, want ids %s; stderr %s", lines, wantIDs, r.stderr)
	}
	if goLockedErr == nil {
		t.Fatal("Go read the locked container")
	}
	// Five crates on the first pass, then only the folder without one.
	if n := f.crateGets.Load(); n != 6 {
		t.Errorf("PowerShell asked for %d crates, want 6", n)
	}
	if f.badAuth.Load() != 0 {
		t.Errorf("PowerShell sent a wrong bearer token")
	}
	if d := mapDiff(treeOf(t, goCache), treeOf(t, psCache)); d != "" {
		t.Errorf("caches differ:\n%s", d)
	}
}

func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	got := map[string]string{}
	must(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			got[filepath.ToSlash(rel)+"/"] = "dir"
			return nil
		}
		raw, err := os.ReadFile(path)
		got[filepath.ToSlash(rel)] = string(raw)
		return err
	}))
	return got
}

func TestConformanceBrowseListingErrors(t *testing.T) {
	powershell(t)
	f := newFakeAccount(t)
	f.denyList = true
	svc, err := service.NewClientWithNoCredential(f.srv.URL+"/", nil)
	must(t, err)
	_, goErr := listContainers(context.Background(), svc)
	if goErr == nil {
		t.Fatal("Go listed the containers")
	}
	want := fmt.Errorf("listing the containers in %s needs read access to the whole account; name them with --container instead: %w", "acc", goErr).Error()

	home := t.TempDir()
	r := psCall(t, psHomeEnv(home), psStubSignIn+fmt.Sprintf(`
		function Get-LbfBrowseAccountUrl { param($Account) %s }
		try { Invoke-Lbf @('browse', '--tag', 'tag=storage-test') } catch { [Console]::Out.Write((Get-LbfErrorText $_)) }`, psQuote(f.srv.URL)))
	if r.stdout != want {
		t.Errorf("error differs:\n%s", lineDiff(want, r.stdout))
	}
}

func TestConformanceBrowseListenErrors(t *testing.T) {
	powershell(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	defer busy.Close()
	_, port, _ := net.SplitHostPort(busy.Addr().String())
	ports := []string{port, "70000", "-1", "abc", "99999999999999999999999"}
	var calls []string
	for _, p := range ports {
		calls = append(calls, fmt.Sprintf(`try { Start-LbfCatalogueServer (New-LbfCatalogue) 'acc' %s } catch { 'ERR ' + (Get-LbfErrorText $_) }`, psQuote(p)))
	}
	r := psCall(t, nil, strings.Join(calls, "\n"))
	got := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	for i, p := range ports {
		_, err := net.Listen("tcp", "127.0.0.1:"+p)
		if err == nil {
			t.Fatalf("Go listened on %s", p)
		}
		if i >= len(got) || got[i] != "ERR "+err.Error() {
			t.Errorf("port %s: go %q, ps %q", p, "ERR "+err.Error(), got)
		}
	}
}

// Starts lbf browse in PowerShell against f, returning the URL it serves and a func that stops it and returns its stderr.
func startPSBrowse(t *testing.T, f *fakeAccount, home string) (string, func() string) {
	t.Helper()
	ps := powershell(t)
	script := psScriptVersion(t, "dev")
	command := fmt.Sprintf("$env:LBF_TEST_NO_MAIN = '1'; . %s; Remove-Item Env:LBF_TEST_NO_MAIN\n%s\nfunction Get-LbfBrowseAccountUrl { param($Account) %s }\nInvoke-LbfMain -Raw @('browse', '--tag', 'tag=storage-test')",
		psQuote(script), psStubSignIn, psQuote(f.srv.URL))
	cmd := psCommand(ps, command)
	cmd.Env = psEnviron(psHomeEnv(home))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	must(t, err)
	must(t, cmd.Start())
	stop := func() string {
		cmd.Process.Kill()
		cmd.Wait()
		return strings.ReplaceAll(stderr.String(), "\r\n", "\n")
	}
	t.Cleanup(func() { stop() })
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		lines <- strings.TrimRight(line, "\r\n")
		io.Copy(io.Discard, out)
	}()
	select {
	case line := <-lines:
		m := regexp.MustCompile(`^Browsing acc at (http://127\.0\.0\.1:[0-9]+/) \(Ctrl-C to stop\)$`).FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("browse printed %q\nstderr:\n%s", line, stop())
		}
		return m[1], stop
	case <-time.After(2 * time.Minute):
		t.Fatalf("browse printed nothing\nstderr:\n%s", stop())
	}
	return "", nil
}

// The catalogue Go's browse builds from the same cached crates.
func goCatalogue(t *testing.T, cacheRoot, account, tag string, containers []string, f *fakeAccount) (catalogue, string) {
	t.Helper()
	cat := catalogue{Files: map[string][]catalogFile{}, Labels: map[string]string{}, crates: map[string]string{}}
	var log strings.Builder
	for _, name := range containers {
		if f.locked[name] {
			fmt.Fprintf(&log, "Skipping %s: you cannot read it\n", name)
			continue
		}
		cache := filepath.Join(cacheRoot, "lbf", "crates", account, name)
		for _, id := range slices.Sorted(maps.Keys(f.crates[name])) {
			path := filepath.Join(cache, id, crateName)
			raw, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				continue
			}
			must(t, err)
			row, files, names, err := flattenCrate(raw)
			if err != nil {
				fmt.Fprintf(&log, "Skipping %s/%s: %v\n", name, id, err)
				continue
			}
			row["id"], row["container"] = id, name
			row["fetch"] = fetchCommand(id, name, tag, account)
			cat.Datasets = append(cat.Datasets, row)
			cat.Files[name+"/"+id] = files
			cat.crates[name+"/"+id] = path
			for iri, l := range names {
				if cat.Labels[iri] == "" {
					cat.Labels[iri] = l
				}
			}
		}
	}
	fmt.Fprintf(&log, "%d datasets in %s\n", len(cat.Datasets), account)
	return cat, log.String()
}

type rawResponse struct {
	status int
	header http.Header
	body   string
}

// Sends each raw request on one connection and reads the responses.
func sendRaw(t *testing.T, base string, reqs ...string) []rawResponse {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimSuffix(strings.TrimPrefix(base, "http://"), "/"))
	must(t, err)
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Minute))
	br := bufio.NewReader(conn)
	var out []rawResponse
	for _, raw := range reqs {
		_, err := io.WriteString(conn, raw)
		must(t, err)
		method, _, _ := strings.Cut(raw, " ")
		resp, err := http.ReadResponse(br, &http.Request{Method: method})
		if err != nil {
			line, _, _ := strings.Cut(raw, "\r\n")
			t.Fatalf("%s %s: %v", base, line, err)
		}
		body, err := io.ReadAll(resp.Body)
		must(t, err)
		resp.Body.Close()
		for _, h := range []string{"Date", "Content-Length", "Transfer-Encoding", "Connection"} {
			resp.Header.Del(h)
		}
		out = append(out, rawResponse{resp.StatusCode, resp.Header, string(body)})
	}
	return out
}

func TestConformanceBrowseServesTheCatalogue(t *testing.T) {
	powershell(t)
	crates := browseCrates(t)
	f := newFakeAccount(t)
	f.containers = []string{"bronze", "empty", "locked", "silver"}
	f.crates["bronze"] = map[string][]byte{"20260102-a-b-1234": crates["derived"], "20260102-e-f-9abc": crates["profiled"], "broken": crates["not json"], "partway": nil, "zz-iri": crates["iri links"]}
	f.crates["empty"] = map[string][]byte{}
	f.crates["silver"] = map[string][]byte{"20260102-c-d-5678": crates["taught nothing"], "runs": crates["runs"]}
	f.locked["locked"] = true
	home := t.TempDir()

	base, stop := startPSBrowse(t, f, home)
	cacheRoot := psCacheRoot(home)
	cat, wantLog := goCatalogue(t, cacheRoot, "acc", "tag=storage-test", f.containers, f)
	if _, err := os.Stat(filepath.Join(cacheRoot, "lbf", "crates", "acc", "bronze", "partway")); !os.IsNotExist(err) {
		t.Errorf("cached a folder with no crate: %v", err)
	}
	if len(cat.Datasets) != 5 {
		t.Fatalf("expected 5 datasets from the cache, got %d", len(cat.Datasets))
	}
	handler, err := catalogueHandler(cat)
	must(t, err)
	goSrv := httptest.NewServer(handler)
	defer goSrv.Close()

	crate := "/crate/bronze/20260102-a-b-1234"
	info, err := os.Stat(cat.crates["bronze/20260102-a-b-1234"])
	must(t, err)
	later := info.ModTime().UTC().Add(time.Hour).Format(http.TimeFormat)
	earlier := info.ModTime().UTC().Add(-time.Hour).Format(http.TimeFormat)
	get := func(target string, extra ...string) string {
		return "GET " + target + " HTTP/1.1\r\nHost: 127.0.0.1\r\n" + strings.Join(extra, "") + "\r\n"
	}
	reqs := []string{
		get("/"),
		get("/catalogue.json"),
		get(crate),
		get("/crate/silver/runs?x=1"),
		get(crate, "If-Modified-Since: "+later+"\r\n"),
		get(crate, "If-Modified-Since: "+earlier+"\r\n"),
		get("/crate/bronze/partway"),
		get("/crate/bronze/broken"),
		get("/crate/nope/20260102-a-b-1234"),
		get("/crate/bronze/"),
		get("/crate/bronze/a/b"),
		get("/nope"),
		get("/catalogue.json/"),
		get("//catalogue.json?a=b"),
		get("/a/../crate/./bronze/20260102-a-b-1234"),
		get("/crate/b%72onze/20260102-a-b-1234"),
		get("/crate/BRONZE/20260102-a-b-1234"),
		get("/Catalogue.json"),
		"HEAD / HTTP/1.1\r\nHost: x\r\n\r\n",
		"HEAD //x HTTP/1.1\r\nHost: x\r\n\r\n",
		"POST /catalogue.json HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n",
		"DELETE " + crate + " HTTP/1.1\r\nHost: x\r\n\r\n",
		"PUT /nope HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n",
		"get / HTTP/1.1\r\nHost: x\r\n\r\n",
	}
	goResp := sendRaw(t, goSrv.URL, reqs...)
	psResp := sendRaw(t, base, reqs...)
	for i, req := range reqs {
		g, p := goResp[i], psResp[i]
		line, _, _ := strings.Cut(req, "\r\n")
		if g.status != p.status {
			t.Errorf("%s: status go %d, ps %d", line, g.status, p.status)
		}
		if !maps.EqualFunc(g.header, p.header, slices.Equal) {
			t.Errorf("%s: headers\n go: %v\n ps: %v", line, g.header, p.header)
		}
		if g.body != p.body {
			t.Errorf("%s: body differs:\n%s", line, lineDiff(g.body, p.body))
		}
	}
	if string(psResp[0].body) != string(browsePage) {
		t.Errorf("the page is not browse.html")
	}

	// Several connections at once, one idle, as browsers open them.
	idle, err := net.Dial("tcp", strings.TrimSuffix(strings.TrimPrefix(base, "http://"), "/"))
	must(t, err)
	defer idle.Close()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			resp, err := http.Get(base + "catalogue.json")
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		})
	}
	wg.Wait()

	stderr := stop()
	if want := wantLog + "opened " + base + "\n"; stderr != want {
		t.Errorf("stderr differs:\n%s", lineDiff(want, stderr))
	}

	// Crates are cached for good; a folder without one is asked again.
	gets := f.crateGets.Load()
	base2, stop2 := startPSBrowse(t, f, home)
	if base2 == "" {
		t.Fatal("second browse did not start")
	}
	stop2()
	if n := f.crateGets.Load() - gets; n != 1 {
		t.Errorf("second browse asked for %d crates, want 1", n)
	}
}

// ---------- upgrade ----------

func psFakeReleases(t *testing.T, tag string, bin []byte, sum string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var heads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			heads.Add(1)
			http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
		case "/releases/download/" + tag + "/SHA256SUMS":
			fmt.Fprintf(w, "%s  lbf-other-os\r\n%s  lbf.ps1\n%s *lbf-windows-amd64.exe\n", strings.Repeat("0", 64), sum, strings.Repeat("1", 64))
		case "/releases/download/" + tag + "/lbf.ps1":
			w.Write(bin)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &heads
}

func TestConformanceUpgradeNewerVersion(t *testing.T) {
	cases := [][2]string{
		{"v0.1.6", "v0.1.5"}, {"v0.2.0", "v0.1.10"}, {"v0.1.10", "v0.1.9"}, {"v0.1.5", "v0.1.5"}, {"v0.1.4", "v0.1.5"},
		{"v0.1.6", "dev"}, {"", "v0.1.5"}, {"v0.2.0", "v0.2.0-rc1"}, {"v0.2.1", "v0.2.0-rc1"}, {"v0.1.9", "v0.2.0-rc1"},
		{"v0.2.0-rc1", "v0.2.0"}, {"v0.2.0-rc1", "v0.1.9"}, {"v0.2.0-rc2", "v0.2.0-rc1"}, {"v0.1.6x", "v0.1.5"},
		{"v0.1.6", "v0.1.5x"}, {"v0.1", "v0.0.9"}, {"v0.1.6.1", "v0.1.5"}, {"v0.1.-6", "v0.1.5"},
		{"v1.0.0", "v1.0.0-rc.1"}, {"v1.0.0", "v1.0.0-alpha.beta"}, {"v1.0.0+b", "v0.9.0"}, {"v1.0.0", "v0.9.0+b"},
		{"v01.0.0", "v0.9.0"}, {"v10.0.0", "v9.99.99"}, {"v1.0.0", "v1.0.0-01"}, {"v1.0.1", "v1.0.0-x.01"}, {"1.0.0", "v0.1.0"},
	}
	var calls []string
	for _, c := range cases {
		calls = append(calls, fmt.Sprintf("Test-LbfNewerVersion %s %s", psQuote(c[0]), psQuote(c[1])))
	}
	r := psCall(t, nil, strings.Join(calls, "\n"))
	got := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	for i, c := range cases {
		want := "False"
		if newerVersion(c[0], c[1]) {
			want = "True"
		}
		if i >= len(got) || got[i] != want {
			t.Errorf("newerVersion(%q, %q): go %s, ps %v", c[0], c[1], want, got)
		}
	}
}

func TestConformanceUpgradeRejectsChecksumMismatch(t *testing.T) {
	srv, _ := psFakeReleases(t, "v0.2.0", []byte("tampered"), strings.Repeat("a", 64))
	path := filepath.Join(t.TempDir(), "lbf.ps1")
	writeFile(t, path, "old lbf")
	r := psCall(t, nil, fmt.Sprintf(`try { Invoke-LbfUpgrade -Base %s -Current 'v0.1.5' -Path %s } catch { Get-LbfErrorText $_ }`, psQuote(srv.URL+"/releases"), psQuote(path)))
	if want := "downloaded lbf.ps1 does not match its published checksum; nothing was changed\n"; r.stdout != want {
		t.Fatalf("got %q, want %q", r.stdout, want)
	}
	if got, _ := os.ReadFile(path); string(got) != "old lbf" {
		t.Fatalf("script changed to %q", got)
	}
}

func TestConformanceUpgradeMessagesMatchGo(t *testing.T) {
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/none/releases/latest":
			w.WriteHeader(http.StatusNotFound)
		case "/nosums/releases/latest":
			http.Redirect(w, r, "/nosums/releases/tag/v9.0.0", http.StatusFound)
		case "/noentry/releases/latest":
			http.Redirect(w, r, "/noentry/releases/tag/v9.0.0", http.StatusFound)
		case "/noentry/releases/download/v9.0.0/SHA256SUMS":
			fmt.Fprintln(w, "abc  lbf-other")
		default:
			http.NotFound(w, r)
		}
	}))
	defer missing.Close()
	for _, base := range []string{"none/releases", "nosums/releases"} {
		_, _, goErr := upgrade(context.Background(), missing.URL+"/"+base, "v0.1.5", filepath.Join(t.TempDir(), "lbf"))
		r := psCall(t, nil, fmt.Sprintf(`try { Invoke-LbfUpgrade -Base %s -Current 'v0.1.5' -Path 'unused' } catch { Get-LbfErrorText $_ }`, psQuote(missing.URL+"/"+base)))
		if goErr == nil || r.stdout != goErr.Error()+"\n" {
			t.Errorf("%s: go %v, ps %q", base, goErr, r.stdout)
		}
	}
	r := psCall(t, nil, fmt.Sprintf(`try { Invoke-LbfUpgrade -Base %s -Current 'v0.1.5' -Path 'unused' } catch { Get-LbfErrorText $_ }`, psQuote(missing.URL+"/noentry/releases")))
	if want := "SHA256SUMS has no entry for lbf.ps1\n"; r.stdout != want {
		t.Errorf("got %q, want %q", r.stdout, want)
	}
}

func TestConformanceUpgradeLatestVersionIsCached(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/releases/tag/v0.3.0", http.StatusFound)
	}))
	defer srv.Close()
	cache := filepath.Join(t.TempDir(), "lbf", "latest-version")
	call := fmt.Sprintf(`Get-LbfCachedLatestVersion %s %s`, psQuote(srv.URL+"/releases"), psQuote(cache))

	r := psCall(t, nil, call+"\n"+call)
	if r.stdout != "v0.3.0\nv0.3.0\n" || hits.Load() != 1 {
		t.Fatalf("got %q after %d requests, want two v0.3.0 from one", r.stdout, hits.Load())
	}
	if got, _ := os.ReadFile(cache); string(got) != "v0.3.0" {
		t.Fatalf("cache holds %q", got)
	}
	stale := time.Now().Add(-2 * updateCheckEvery)
	must(t, os.Chtimes(cache, stale, stale))
	srv.Close()
	if r := psCall(t, nil, call); r.stdout != "v0.3.0\n" {
		t.Fatalf("offline refresh lost the cached version: %q", r.stdout)
	}
	if info, err := os.Stat(cache); err != nil || time.Since(info.ModTime()) > time.Minute {
		t.Errorf("an offline check did not restart the hour: %v", err)
	}
}

func TestConformanceUpgradeUpdateCheckGivesUpOnASilentServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	r := psCall(t, nil, fmt.Sprintf(`$sw = [Diagnostics.Stopwatch]::StartNew()
		$got = Get-LbfCachedLatestVersion %s %s
		"[$got] $([int]$sw.Elapsed.TotalMilliseconds)"`, psQuote(srv.URL+"/releases"), psQuote(filepath.Join(t.TempDir(), "latest-version"))))
	var got string
	var ms int
	if _, err := fmt.Sscanf(r.stdout, "%s %d", &got, &ms); err != nil || got != "[]" || time.Duration(ms)*time.Millisecond > updateCheckLimit+time.Second {
		t.Fatalf("got %q", r.stdout)
	}
}

func TestConformanceUpgradeIntoReadOnlyFolderSaysWhatToDo(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs Unix permissions")
	}
	bin := []byte("new lbf")
	sum := sha256.Sum256(bin)
	goSrv := fakeReleases(t, "v0.2.0", bin, hex.EncodeToString(sum[:]))
	psSrv, _ := psFakeReleases(t, "v0.2.0", bin, hex.EncodeToString(sum[:]))
	dir := t.TempDir()
	exe := filepath.Join(dir, "lbf")
	writeFile(t, exe, "old lbf")
	os.Chmod(dir, 0o555)
	defer os.Chmod(dir, 0o755)

	_, _, goErr := upgrade(context.Background(), goSrv.URL+"/releases", "v0.1.5", exe)
	r := psCall(t, nil, fmt.Sprintf(`try { Invoke-LbfUpgrade -Base %s -Current 'v0.1.5' -Path %s } catch { Get-LbfErrorText $_ }`, psQuote(psSrv.URL+"/releases"), psQuote(exe)))
	if goErr == nil || !strings.Contains(goErr.Error(), "README") || r.stdout != goErr.Error()+"\n" {
		t.Fatalf("go %v\nps %q", goErr, r.stdout)
	}
}

// Users run lbf upgrade and see the update warning through the script itself.
func TestConformanceUpgradeCommand(t *testing.T) {
	powershell(t)
	newer, err := os.ReadFile(psScriptVersion(t, "v0.2.0"))
	must(t, err)
	sum := sha256.Sum256(newer)
	srv, heads := psFakeReleases(t, "v0.2.0", newer, hex.EncodeToString(sum[:]))
	old, err := os.ReadFile(psScriptVersion(t, "v0.1.5"))
	must(t, err)
	home := t.TempDir()
	script := filepath.Join(t.TempDir(), "lbf.ps1")
	must(t, os.WriteFile(script, old, 0o644))
	env := psHomeEnv(home)
	env["LBF_TEST_RELEASES"] = srv.URL + "/releases"
	env["LBF_NO_UPDATE_CHECK"] = ""
	cache := filepath.Join(psCacheRoot(home), "lbf", "latest-version")

	r := psRunScript(t, script, env, "help")
	if r.code != 0 || r.stderr != upgradeWarning("v0.1.5", "v0.2.0") {
		t.Fatalf("help: exit %d, stderr:\n%s", r.code, lineDiff(upgradeWarning("v0.1.5", "v0.2.0"), r.stderr))
	}
	if got, _ := os.ReadFile(cache); string(got) != "v0.2.0" {
		t.Errorf("cache %s holds %q", cache, got)
	}
	if n := heads.Load(); n != 1 {
		t.Errorf("asked for the latest release %d times, want 1", n)
	}
	if r := psRunScript(t, script, env, "version"); r.stdout != "lbf v0.1.5\n" || r.stderr != upgradeWarning("v0.1.5", "v0.2.0") || heads.Load() != 1 {
		t.Errorf("version within the hour: %q %q, %d requests", r.stdout, r.stderr, heads.Load())
	}
	env["LBF_NO_UPDATE_CHECK"] = "1"
	if r := psRunScript(t, script, env, "version"); r.stderr != "" {
		t.Errorf("LBF_NO_UPDATE_CHECK=1 still warned: %q", r.stderr)
	}
	env["LBF_NO_UPDATE_CHECK"] = ""

	r = psRunScript(t, script, env, "upgrade")
	if want := fmt.Sprintf("Upgraded lbf %s -> %s\n", "v0.1.5", "v0.2.0"); r.code != 0 || r.stdout != want || r.stderr != "" {
		t.Fatalf("upgrade: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if got, _ := os.ReadFile(script); !bytes.Equal(got, newer) {
		t.Fatal("the script was not replaced")
	}
	if _, err := os.Stat(script + ".new"); !os.IsNotExist(err) {
		t.Errorf("left %s.new behind", script)
	}
	if r := psRunScript(t, script, env, "version"); r.stdout != "lbf v0.2.0\n" || r.stderr != "" {
		t.Errorf("after upgrade: %q %q", r.stdout, r.stderr)
	}
	if r := psRunScript(t, script, env, "upgrade"); r.stdout != fmt.Sprintf("lbf %s is up to date\n", "v0.2.0") || r.code != 0 {
		t.Errorf("second upgrade: exit %d, %q %q", r.code, r.stdout, r.stderr)
	}
}
