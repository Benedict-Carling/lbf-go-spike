package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
)

const (
	fixedID         = "20261001-brave-otter-1a2b"
	parentDatasetID = "20260101-quiet-heron-0000"
)

var (
	reDone    = regexp.MustCompile(`Done in [^\n]*`)
	reCount   = regexp.MustCompile(`\[[0-9]+/`)
	reAnyID   = regexp.MustCompile(`[0-9]{8}-[a-z]+-[a-z]+-[0-9a-f]{4}`)
	rePort    = regexp.MustCompile(`127\.0\.0\.1:[0-9]+`)
	reExpiry  = regexp.MustCompile(`se=[^&"]+`)
	rePartial = regexp.MustCompile(`\.partial-[a-z2-7]{8}`)
	// Azure's error bodies name each request.
	reRequest = regexp.MustCompile(`RequestId:[0-9a-f-]+\nTime:[^<]+`)
)

// The container of w through Azurite's account key, for setups that write what lbf never would.
func adminContainer(t *testing.T, w *world) *container.Client {
	t.Helper()
	cred, err := container.NewSharedKeyCredential(emulatorAccount, emulatorKey)
	must(t, err)
	cc, err := container.NewClientWithSharedKeyCredential(emulatorURL+"/"+w.container, cred, nil)
	must(t, err)
	return cc
}

func putBlob(t *testing.T, w *world, name, body string, meta map[string]string) {
	t.Helper()
	m := map[string]*string{}
	for k, v := range meta {
		m[k] = to.Ptr(v)
	}
	_, err := adminContainer(t, w).NewBlockBlobClient(name).UploadBuffer(context.Background(), []byte(body), &blockblob.UploadBufferOptions{Metadata: m})
	must(t, err)
}

func readBlob(t *testing.T, w *world, name string) string {
	t.Helper()
	resp, err := adminContainer(t, w).NewBlobClient(name).DownloadStream(context.Background(), nil)
	must(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	must(t, err)
	return string(b)
}

func sum256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Runs the Go lbf in a setup and fails unless it succeeds.
func goOK(t *testing.T, w *world, args ...string) {
	t.Helper()
	if r := w.lbfGo(t, nil, args...); r.code != 0 {
		t.Fatalf("setup %v: exit %d\n%s", args, r.code, r.stderr)
	}
}

func goPublish(id string, extra ...string) func(*testing.T, *world) {
	return func(t *testing.T, w *world) {
		goOK(t, w, append([]string{"publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}", "--id", id}, extra...)...)
	}
}

func setSASEnv(t *testing.T, w *world, key, value string) {
	t.Helper()
	raw, err := os.ReadFile(w.sasEnv)
	must(t, err)
	re := regexp.MustCompile(`(?m)^` + key + `='[^']*'$`)
	must(t, os.WriteFile(w.sasEnv, re.ReplaceAll(raw, []byte(key+"='"+value+"'")), 0o600))
}

func writeSASFile(t *testing.T, w *world, body string) {
	t.Helper()
	writeFile(t, filepath.Join(w.dir, "creds.env"), body)
}

func steps(fs ...func(*testing.T, *world)) func(*testing.T, *world) {
	return func(t *testing.T, w *world) {
		for _, f := range fs {
			f(t, w)
		}
	}
}

func lbfCrate(id, ctx string, graph ...string) string {
	return `{"@context": "` + ctx + `", "@graph": [` + strings.Join(graph, ", ") + `]}`
}

// Proxies Azurite, calling hook on each request before forwarding it; dropping the response when hook says so.
func proxyAzurite(t *testing.T, w *world, hook func(*http.Request) bool) {
	t.Helper()
	backend, err := url.Parse("http://127.0.0.1:10000")
	must(t, err)
	forward := httputil.NewSingleHostReverseProxy(backend)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		resp := httptest.NewRecorder()
		drop := hook(r)
		forward.ServeHTTP(resp, r)
		if drop {
			if conn, _, err := http.NewResponseController(rw).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		for k, v := range resp.Header() {
			rw.Header()[k] = v
		}
		rw.WriteHeader(resp.Code)
		rw.Write(resp.Body.Bytes())
	}))
	t.Cleanup(srv.Close)
	w.env = append(w.env, "LBF_BLOB_ENDPOINT="+srv.URL+"/"+emulatorAccount)
}

// Proxies Azurite, answering the requests busy picks with 503 Server Busy as Azure does under load.
func proxyBusy(t *testing.T, w *world, busy func(*http.Request) bool) {
	t.Helper()
	backend, err := url.Parse("http://127.0.0.1:10000")
	must(t, err)
	forward := httputil.NewSingleHostReverseProxy(backend)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if busy(r) {
			rw.Header().Set("x-ms-error-code", "ServerBusy")
			rw.Header().Set("Content-Type", "application/xml")
			rw.WriteHeader(http.StatusServiceUnavailable)
			if r.Method != http.MethodHead {
				io.WriteString(rw, "\ufeff<?xml version=\"1.0\" encoding=\"utf-8\"?><Error><Code>ServerBusy</Code><Message>The server is busy.</Message></Error>")
			}
			return
		}
		forward.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	w.env = append(w.env, "LBF_BLOB_ENDPOINT="+srv.URL+"/"+emulatorAccount)
}

func firstN(n int32, match func(*http.Request) bool) func(*http.Request) bool {
	var seen atomic.Int32
	return func(r *http.Request) bool { return match(r) && seen.Add(1) <= n }
}

func isList(r *http.Request) bool { return r.URL.Query().Get("comp") == "list" }

func onceWhen(match func(*http.Request) bool) func(*http.Request) bool {
	var done atomic.Bool
	return func(r *http.Request) bool { return match(r) && done.CompareAndSwap(false, true) }
}

func isCommit(r *http.Request) bool {
	return r.Method == http.MethodPut && r.URL.Query().Get("comp") == "blocklist"
}

func isCratePut(r *http.Request) bool {
	return r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/"+crateName)
}

var roundTripFiles = map[string]string{
	"data/run1/a.txt":                 "alpha\n",
	"data/run1/empty.txt":             "",
	"data/run1/sub/b.csv":             "x,y\n1,2\n",
	"data/run1/sub/deeper/c.json":     `{"k": "v"}`,
	"data/run1/ünï cødé/细胞 图像.tiff":   "tiff-ish",
	"data/run1/space name.txt":        "s",
	"data/run1/odd #%+&=@;,!'()$.dat": "odd",
	"data/run1/Zebra.TXT":             "z",
}

func withFiles(base map[string]string, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func manyFiles(n int) map[string]string {
	files := map[string]string{}
	for i := range n {
		files[fmt.Sprintf("data/run1/well%02d/f%04d.csv", i%20, i)] = fmt.Sprintf("%d,%d\n", i, i*i)
	}
	return files
}

func bigFile(size int) string {
	var b strings.Builder
	for b.Len() < size {
		fmt.Fprintf(&b, "%016x", b.Len())
	}
	return b.String()[:size]
}

// Runs each case in parallel; Azure's request IDs are normalized, and blobs compared only where the SAS can list them.
func conformAll(t *testing.T, cases map[string]conformCase) {
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c.normalize = append(c.normalize, reRequest)
			if c.perms != "" && !strings.Contains(c.perms, "l") {
				c.ignoreBlobs = true
			}
			conform(t, c)
		})
	}
}

func TestConformanceTransferSASEnv(t *testing.T) {
	conformAll(t, transferSASEnvCases())
}

func transferSASEnvCases() map[string]conformCase {
	expired := func(t *testing.T, w *world) { setSASEnv(t, w, "AZURE_SAS_EXPIRY", "2020-01-01T00:00:00Z") }
	cases := map[string]conformCase{
		"missing file":         {args: []string{"fetch", fixedID, "--sas-env", "nope.env"}},
		"folder":               {files: map[string]string{"dir/x": ""}, args: []string{"fetch", fixedID, "--sas-env", "dir"}},
		"not key value":        {setup: func(t *testing.T, w *world) { writeSASFile(t, w, "export AZURE_ACCOUNT=x\n") }, args: []string{"check", "--mode", "download", "--sas-env", "creds.env"}},
		"long bad line":        {setup: func(t *testing.T, w *world) { writeSASFile(t, w, "AZURE_ACCOUNT='"+strings.Repeat("é", 70)+"\n") }, args: []string{"check", "--mode", "download", "--sas-env", "creds.env"}},
		"control characters":   {setup: func(t *testing.T, w *world) { writeSASFile(t, w, "\tAZURE_ACCOUNT\x01='x'\u202e\n") }, args: []string{"check", "--mode", "download", "--sas-env", "creds.env"}},
		"unexpected key":       {setup: func(t *testing.T, w *world) { writeSASFile(t, w, "PATH='/tmp'\n") }, args: []string{"check", "--mode", "download", "--sas-env", "creds.env"}},
		"duplicate key":        {setup: func(t *testing.T, w *world) { writeSASFile(t, w, "AZURE_ACCOUNT='a'\nAZURE_ACCOUNT='b'\n") }, args: []string{"check", "--mode", "download", "--sas-env", "creds.env"}},
		"missing value":        {setup: func(t *testing.T, w *world) { writeSASFile(t, w, "\n\nAZURE_ACCOUNT='abc'  \n\r\nAZURE_SAS=''\n") }, args: []string{"check", "--mode", "download", "--sas-env", "creds.env"}},
		"bad account":          {perms: "rl", setup: func(t *testing.T, w *world) { setSASEnv(t, w, "AZURE_ACCOUNT", "Bad_Account") }, args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
		"unreadable expiry":    {perms: "rl", setup: func(t *testing.T, w *world) { setSASEnv(t, w, "AZURE_SAS_EXPIRY", "tomorrow") }, args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
		"impossible date":      {perms: "rl", setup: func(t *testing.T, w *world) { setSASEnv(t, w, "AZURE_SAS_EXPIRY", "2099-02-30T00:00:00Z") }, args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
		"expired":              {perms: "rl", setup: expired, args: []string{"fetch", fixedID, "--sas-env", "{sas}", "--container", "{container}"}},
		"expired for upload":   {perms: "rlw", files: roundTripFiles, setup: expired, args: []string{"publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}"}},
		"read-only for upload": {perms: "rl", files: roundTripFiles, args: []string{"publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}"}},
		"other container":      {perms: "rl", args: []string{"fetch", fixedID, "--sas-env", "{sas}", "--container", "silver"}},
		"crlf and offset expiry": {perms: "rl", setup: func(t *testing.T, w *world) {
			raw, err := os.ReadFile(w.sasEnv)
			must(t, err)
			s := strings.ReplaceAll(string(raw), "\n", "\r\n")
			s = regexp.MustCompile(`AZURE_SAS_EXPIRY='[^']*'`).ReplaceAllString(s, "AZURE_SAS_EXPIRY='2099-06-01T12:00:00.5+01:00'")
			must(t, os.WriteFile(w.sasEnv, []byte(s), 0o600))
		}, args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
	}
	return cases
}

func TestConformanceTransferWriteSASEnv(t *testing.T) {
	ps := powershell(t)
	script, err := builtScript()
	must(t, err)
	dir := t.TempDir()
	in := target{Account: "examplestorage1", Container: "bronze", SAS: "sv=x&sig=y", Expiry: time.Date(2026, 11, 26, 9, 30, 0, 0, time.FixedZone("", 3600)),
		Permissions: "rlw", User: "u@example.com", SubscriptionName: "Sub One", SubscriptionID: "id"}
	goPath := filepath.Join(dir, "go.env")
	must(t, writeSASEnv(goPath, in))
	psPath := filepath.Join(dir, "ps.env")
	command := fmt.Sprintf(`$env:LBF_TEST_NO_MAIN = '1'; . '%s'; $t = New-LbfTarget -Account examplestorage1 -Container bronze -SAS 'sv=x&sig=y' -Expiry ([DateTimeOffset]::new(2026, 11, 26, 9, 30, 0, [TimeSpan]::FromHours(1))) -Permissions rlw -User u@example.com -SubscriptionName 'Sub One' -SubscriptionID id; Write-LbfSasEnv -Path '%s' -Target $t; $t.User = "a'b"; try { Write-LbfSasEnv -Path '%s' -Target $t } catch { [Console]::Out.Write($_.Exception.Message) }`, script, psPath, psPath)
	out, err := runPS(t, ps, command)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	in.User = "a'b"
	if goErr := writeSASEnv(filepath.Join(dir, "x.env"), in); goErr == nil || goErr.Error() != out {
		t.Errorf("error: go %v, ps %q", goErr, out)
	}
	g, _ := os.ReadFile(goPath)
	p, _ := os.ReadFile(psPath)
	if !bytes.Equal(g, p) {
		t.Errorf("files differ:\ngo %q\nps %q", g, p)
	}
	if gi, gerr := os.Stat(goPath); gerr == nil {
		if pi, perr := os.Stat(psPath); perr == nil && pi.Mode().Perm() != gi.Mode().Perm() {
			t.Errorf("ps wrote mode %v, go %v", pi.Mode().Perm(), gi.Mode().Perm())
		}
	}
}

func runPS(t *testing.T, ps, command string) (string, error) {
	t.Helper()
	cmd := psCommand(ps, command)
	cmd.Env = append(os.Environ(), "LBF_NO_UPDATE_CHECK=1", "TERM=dumb")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestConformanceTransferCheck(t *testing.T) {
	conformAll(t, transferCheckCases())
}

func transferCheckCases() map[string]conformCase {
	deleteContainer := func(t *testing.T, w *world) {
		_, err := adminContainer(t, w).Delete(context.Background(), nil)
		must(t, err)
	}
	claimWrite := func(t *testing.T, w *world) { setSASEnv(t, w, "AZURE_SAS_PERMISSIONS", "rlw") }
	serverExpired := func(t *testing.T, w *world) {
		cred, err := container.NewSharedKeyCredential(emulatorAccount, emulatorKey)
		must(t, err)
		qp, err := sas.BlobSignatureValues{Protocol: sas.ProtocolHTTPSandHTTP, StartTime: time.Now().Add(-2 * time.Hour), ExpiryTime: time.Now().Add(-time.Hour),
			Permissions: (&sas.ContainerPermissions{Read: true, List: true}).String(), ContainerName: w.container}.SignWithSharedKey(cred)
		must(t, err)
		setSASEnv(t, w, "AZURE_SAS", qp.Encode())
	}
	cases := map[string]conformCase{
		"upload with write":            {perms: "rlw", args: []string{"check", "--mode", "upload", "--sas-env", "{sas}", "--container", "{container}"}},
		"download with read":           {perms: "rl", args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
		"download with write too":      {perms: "rlw", args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
		"upload with read only":        {perms: "rl", args: []string{"check", "--mode", "upload", "--sas-env", "{sas}", "--container", "{container}"}},
		"upload with write claimed":    {perms: "rl", setup: claimWrite, args: []string{"check", "--mode", "upload", "--sas-env", "{sas}", "--container", "{container}"}},
		"download without list":        {ignoreBlobs: true, perms: "r", args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
		"upload without list":          {ignoreBlobs: true, perms: "rw", args: []string{"check", "--mode", "upload", "--sas-env", "{sas}", "--container", "{container}"}},
		"missing container":            {ignoreBlobs: true, perms: "rl", setup: deleteContainer, args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
		"missing container for upload": {ignoreBlobs: true, perms: "rlw", setup: deleteContainer, args: []string{"check", "--mode", "upload", "--sas-env", "{sas}", "--container", "{container}"}},
		"busy then not": {perms: "rl", setup: func(t *testing.T, w *world) { proxyBusy(t, w, firstN(2, isList)) }, normalize: []*regexp.Regexp{rePort},
			args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
		"busy throughout": {perms: "rlw", setup: func(t *testing.T, w *world) {
			proxyBusy(t, w, func(r *http.Request) bool { return r.URL.Query().Get("comp") == "block" })
		}, normalize: []*regexp.Regexp{rePort}, args: []string{"check", "--mode", "upload", "--sas-env", "{sas}", "--container", "{container}"}},
		"sas expired at the server": {perms: "rl", setup: serverExpired, args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
		"nothing listening": {perms: "rl", env: map[string]string{"LBF_BLOB_ENDPOINT": "http://127.0.0.1:1/devstoreaccount1"}, normalize: []*regexp.Regexp{reExpiry},
			args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}},
	}
	return cases
}

func TestConformanceTransferFetch(t *testing.T) {
	conformAll(t, transferFetchCases())
}

func transferFetchCases() map[string]conformCase {
	ids := []*regexp.Regexp{reDone, reCount}
	fetch := []string{"fetch", fixedID, "--sas-env", "{sas}", "--container", "{container}", "--out", "out"}
	fetchJSON := append(append([]string{}, fetch...), "--json")
	published := goPublish(fixedID)
	tamper := func(rel, body string) func(*testing.T, *world) {
		return func(t *testing.T, w *world) {
			putBlob(t, w, fixedID+"/"+rel, body, map[string]string{"sha256": sum256(body)})
		}
	}
	crafted := func(crate string, blobs map[string]string) func(*testing.T, *world) {
		return func(t *testing.T, w *world) {
			for name, body := range blobs {
				putBlob(t, w, fixedID+"/"+name, body, nil)
			}
			putBlob(t, w, fixedID+"/"+crateName, crate, nil)
		}
	}
	root := `{"@id": "./", "@type": "Dataset", "identifier": "` + fixedID + `", "hasPart": [{"@id": "run1/a.txt"}]}`
	fileA := `{"@id": "run1/a.txt", "@type": "File", "contentSize": "1"}`
	oneFile := map[string]string{"run1/a.txt": "a"}
	cases := map[string]conformCase{
		"round trip":            {perms: "rlw", files: roundTripFiles, setup: published, args: fetch, normalize: ids, unorderedStderr: true},
		"round trip json":       {perms: "rlw", files: roundTripFiles, setup: published, args: fetchJSON, normalize: ids, unorderedStderr: true},
		"download alias":        {perms: "rlw", files: roundTripFiles, setup: published, args: []string{"download", fixedID, "--sas-env", "{sas}", "--container", "{container}"}, normalize: ids, unorderedStderr: true},
		"into a new nested out": {perms: "rlw", files: roundTripFiles, setup: published, args: []string{"fetch", fixedID, "--sas-env", "{sas}", "--container", "{container}", "--out", "a/b/../c"}, normalize: ids, unorderedStderr: true},
		"single file dataset": {perms: "rlw", files: map[string]string{"data/one.csv": "1"}, setup: func(t *testing.T, w *world) {
			goOK(t, w, "publish", "data/one.csv", "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID)
		}, args: fetchJSON, normalize: ids},
		"already here": {perms: "rlw", files: roundTripFiles, setup: steps(published, func(t *testing.T, w *world) { goOK(t, w, fetch...) }), args: fetchJSON, normalize: ids},
		"already here but changed": {perms: "rlw", files: roundTripFiles, setup: steps(published, func(t *testing.T, w *world) {
			goOK(t, w, fetch...)
			writeFile(t, filepath.Join(w.dir, "out", fixedID, "run1", "a.txt"), "ALPHA\n")
		}), args: fetch, normalize: ids},
		"already here but shorter": {perms: "rlw", files: roundTripFiles, setup: steps(published, func(t *testing.T, w *world) {
			goOK(t, w, fetch...)
			writeFile(t, filepath.Join(w.dir, "out", fixedID, "run1", "a.txt"), "a")
		}), args: fetch, normalize: ids},
		"already here but stray": {perms: "rlw", files: roundTripFiles, setup: steps(published, func(t *testing.T, w *world) {
			goOK(t, w, fetch...)
			writeFile(t, filepath.Join(w.dir, "out", fixedID, "run1", "sub", "a-stray.txt"), "x")
		}), args: fetch, normalize: ids},
		"already here but missing": {perms: "rlw", files: roundTripFiles, setup: steps(published, func(t *testing.T, w *world) {
			goOK(t, w, fetch...)
			os.Remove(filepath.Join(w.dir, "out", fixedID, "run1", "Zebra.TXT"))
		}), args: fetch, normalize: ids},
		"already here but crate": {perms: "rlw", files: roundTripFiles, setup: steps(published, func(t *testing.T, w *world) {
			goOK(t, w, fetch...)
			writeFile(t, filepath.Join(w.dir, "out", fixedID, crateName), "{}")
		}), args: fetch, normalize: ids},
		"id is a file":    {perms: "rlw", files: withFiles(roundTripFiles, map[string]string{"out/" + fixedID: "x"}), setup: published, args: fetch, normalize: ids},
		"out is a file":   {perms: "rlw", files: withFiles(roundTripFiles, map[string]string{"out": "x"}), setup: published, args: fetch, normalize: ids},
		"no such dataset": {perms: "rlw", args: fetch},
		"unfinished":      {perms: "rlw", setup: tamper("run1/a.txt", "a"), args: fetch},
		"extra blob":      {perms: "rlw", files: roundTripFiles, setup: steps(published, tamper("run1/extra.txt", "e")), args: fetch},
		"missing blob": {perms: "rlw", files: roundTripFiles, setup: steps(published, func(t *testing.T, w *world) {
			adminContainer(t, w).NewBlobClient(fixedID+"/run1/a.txt").Delete(context.Background(), nil)
		}), args: fetch},
		"tampered blob": {perms: "rlw", files: map[string]string{"data/run1/a.txt": "alpha\n"}, setup: steps(published, tamper("run1/a.txt", "ALPHA\n")), args: fetch, normalize: ids},
		"resized blob":  {perms: "rlw", files: map[string]string{"data/run1/a.txt": "alpha\n"}, setup: steps(published, tamper("run1/a.txt", "alphabet\n")), args: fetch, normalize: ids},
		"stale partial folders": {perms: "rlw", files: withFiles(roundTripFiles, map[string]string{"out/" + fixedID + ".partial-deadbeef/x": "", "out/" + fixedID + ".partial-0b5e55ed/y": ""}), setup: steps(published, func(t *testing.T, w *world) {
			old := time.Now().Add(-48 * time.Hour)
			must(t, os.Chtimes(filepath.Join(w.dir, "out", fixedID+".partial-deadbeef"), old, old))
		}), args: fetch, normalize: ids, unorderedStderr: true},
		"lost download response": {perms: "rlw", files: map[string]string{"data/run1/a.txt": "alpha\n", "data/run1/big.bin": bigFile(8<<20 + 5)}, setup: steps(published, func(t *testing.T, w *world) {
			proxyAzurite(t, w, onceWhen(func(r *http.Request) bool {
				return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "big.bin")
			}))
		}), args: fetch, normalize: []*regexp.Regexp{reDone, reCount, rePort}, unorderedStderr: true},
		"busy listing": {perms: "rlw", files: map[string]string{"data/run1/a.txt": "alpha\n"}, setup: steps(published, func(t *testing.T, w *world) { proxyBusy(t, w, firstN(1, isList)) }),
			args: fetchJSON, normalize: []*regexp.Regexp{reDone, reCount, rePort}},
		"without list permission": {perms: "r", args: fetch},
		"missing container":       {ignoreBlobs: true, perms: "rlw", setup: func(t *testing.T, w *world) { adminContainer(t, w).Delete(context.Background(), nil) }, args: fetch},
		"nothing listening":       {perms: "rlw", env: map[string]string{"LBF_BLOB_ENDPOINT": "http://127.0.0.1:1/devstoreaccount1"}, normalize: []*regexp.Regexp{reExpiry}, args: fetch},
		"derived provenance": {perms: "rlw", files: roundTripFiles, setup: goPublish(fixedID, "--derived-from", parentDatasetID, "--instrument", "name=CellProfiler,version=4.2.6,url=https://cellprofiler.org",
			"--instrument", "name=pipe,version=1,url=https://example.org/pipe", "--property", "sample_id=S1", "--property", "plate id=P 1", "--name", "Run <1> & more", "--description", "Cells\u2028imaged"), args: fetchJSON, normalize: ids, unorderedStderr: true},
		"unknown context":    {perms: "rlw", setup: crafted(lbfCrate(fixedID, "https://example.org/other", root, fileA), oneFile), args: fetchJSON, normalize: ids},
		"relative context":   {perms: "rlw", setup: crafted(lbfCrate(fixedID, "other.jsonld", root, fileA), oneFile), args: fetchJSON, normalize: ids},
		"not json":           {perms: "rlw", setup: crafted(`{"@graph": [}`, oneFile), args: fetch},
		"truncated json":     {perms: "rlw", setup: crafted(`{"@graph": [`, oneFile), args: fetch},
		"json array":         {perms: "rlw", setup: crafted(`[1]`, oneFile), args: fetch},
		"graph not a list":   {perms: "rlw", setup: crafted(`{"@graph": {}}`, oneFile), args: fetch},
		"graph of strings":   {perms: "rlw", setup: crafted(`{"@graph": ["x"]}`, oneFile), args: fetch},
		"no files":           {perms: "rlw", setup: crafted(lbfCrate(fixedID, roCrateContext, `{"@id": "./"}`), oneFile), args: fetch},
		"no size":            {perms: "rlw", setup: crafted(lbfCrate(fixedID, roCrateContext, root, `{"@id": "run1/a.txt", "@type": "File"}`), oneFile), args: fetch},
		"numeric size":       {perms: "rlw", setup: crafted(lbfCrate(fixedID, roCrateContext, root, `{"@id": "run1/a.txt", "@type": "File", "contentSize": 1}`), oneFile), args: fetchJSON, normalize: ids},
		"big numeric size":   {perms: "rlw", setup: crafted(lbfCrate(fixedID, roCrateContext, root, `{"@id": "run1/a.txt", "@type": "File", "contentSize": 1e6}`), oneFile), args: fetch},
		"unreadable file id": {perms: "rlw", setup: crafted(lbfCrate(fixedID, roCrateContext, `{"@id": "./", "hasPart": {"@id": "run1/%zz"}}`), oneFile), args: fetch},
		"escaping name":      {perms: "rlw", setup: crafted(lbfCrate(fixedID, roCrateContext, `{"@id": "./", "hasPart": {"@id": "../x"}}`, `{"@id": "../x", "contentSize": "1"}`), map[string]string{"../x": "x"}), args: fetch},
		"names differing in case": {perms: "rlw", setup: crafted(lbfCrate(fixedID, roCrateContext, `{"@id": "./", "hasPart": [{"@id": "run1/A.txt"}, {"@id": "run1/a.txt"}]}`,
			`{"@id": "run1/A.txt", "contentSize": "1"}`, fileA), map[string]string{"run1/A.txt": "A", "run1/a.txt": "a"}), args: fetch, normalize: ids, unorderedStderr: true},
		"name of the crate": {perms: "rlw", setup: crafted(lbfCrate(fixedID, roCrateContext, `{"@id": "./", "hasPart": [{"@id": "RO-Crate-Metadata.json"}]}`,
			`{"@id": "RO-Crate-Metadata.json", "contentSize": "1"}`), map[string]string{"RO-Crate-Metadata.json": "x"}), args: fetch, normalize: ids},
		"no checksums": {perms: "rlw", setup: crafted(lbfCrate(fixedID, roCrateContext, `{"@id": "./", "hasPart": [{"@id": "run1/a.txt"}, {"@id": "run1/b%20c.txt"}]}`,
			fileA, `{"@id": "run1/b%20c.txt", "contentSize": "2", "sha256": "`+sum256("bc")+`"}`), map[string]string{"run1/a.txt": "a", "run1/b c.txt": "bc"}), args: fetchJSON, normalize: ids, unorderedStderr: true},
		"earlier lbf crate": {perms: "rlw", setup: crafted(strings.ReplaceAll(earlierLbfCrate, "20261005-vocal-unicorn-112d", fixedID), map[string]string{"data/a.csv": "x"}), args: fetchJSON, normalize: ids},
		"default name and description": {perms: "rlw", setup: crafted(lbfCrate(fixedID, roCrateContext,
			`{"@id": "./", "identifier": "`+fixedID+`", "name": "run1", "description": "Dataset of 1 file from run1", "hasPart": {"@id": "run1/a.txt"}, "additionalProperty": [{"@id": "#source_path"}, {"@id": "#sample_id"}, {"@id": "#dup1"}, {"@id": "#dup2"}], "conformsTo": [{"@id": "https://example.org/p"}, "loose"]}`,
			`{"@id": "#source_path", "name": "source_path", "value": "/x/run1"}`, `{"@id": "#sample_id", "name": "sample_id", "value": ["S1"]}`,
			`{"@id": "#dup1", "name": "d", "value": "1"}`, `{"@id": "#dup2", "name": "d", "value": "2"}`, fileA), oneFile), args: fetchJSON, normalize: ids},
		"large file": {perms: "rlw", files: map[string]string{"data/run1/big.bin": bigFile(2*(8<<20) + 12345), "data/run1/small.txt": "s"}, setup: published, args: fetch, normalize: ids, unorderedStderr: true},
	}
	return cases
}

func TestConformanceTransferPublish(t *testing.T) {
	conformAll(t, transferPublishCases())
}

func transferPublishCases() map[string]conformCase {
	ids := []*regexp.Regexp{reDone, reCount, reAnyID}
	fixed := []*regexp.Regexp{reDone, reCount}
	publish := []string{"publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID}
	attempt := func(files map[string]string, stored ...string) func(*testing.T, *world) {
		return func(t *testing.T, w *world) {
			for _, rel := range stored {
				body := files["data/"+rel]
				putBlob(t, w, fixedID+"/"+rel, body, map[string]string{"sha256": sum256(body), "uploader": "tester@example.org"})
			}
		}
	}
	changeFile := func(rel, body string) func(*testing.T, *world) {
		return func(t *testing.T, w *world) { writeFile(t, filepath.Join(w.dir, filepath.FromSlash(rel)), body) }
	}
	small := map[string]string{"data/run1/a.txt": "a", "data/run1/sub/b.csv": "b,c", "data/run1/c.txt": "c"}
	cases := map[string]conformCase{
		"round trip":       {perms: "rlw", files: roundTripFiles, args: []string{"publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}"}, normalize: ids, unorderedStderr: true},
		"json":             {perms: "rlw", files: roundTripFiles, args: []string{"publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}", "--json"}, normalize: ids, unorderedStderr: true},
		"upload alias":     {perms: "rlw", files: small, args: []string{"upload", "data/run1", "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID}, normalize: fixed, unorderedStderr: true},
		"one file":         {perms: "rlw", files: map[string]string{"data/one.csv": "1,2"}, args: []string{"publish", "data/one.csv", "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID}, normalize: fixed},
		"only empty files": {perms: "rlw", files: map[string]string{"data/run1/e1": "", "data/run1/e2": ""}, args: publish, normalize: fixed, unorderedStderr: true},
		"with provenance": {perms: "rlw", files: small, normalize: fixed, unorderedStderr: true, args: append(append([]string{}, publish...), "--derived-from", parentDatasetID,
			"--instrument", "name=CellProfiler,version=4.2.6,url=https://cellprofiler.org", "--property", "sample_id=S1", "--name", "Run 1", "--description", "Cells")},
		"again with identical":      {perms: "rlw", files: small, setup: goPublish(fixedID), args: publish, normalize: fixed, unorderedStderr: true},
		"again with json":           {perms: "rlw", files: small, setup: goPublish(fixedID), args: append(append([]string{}, publish...), "--json"), normalize: fixed, unorderedStderr: true},
		"again with other property": {perms: "rlw", files: small, setup: goPublish(fixedID, "--property", "sample_id=S1"), args: append(append([]string{}, publish...), "--property", "sample_id=S2"), normalize: fixed, unorderedStderr: true},
		"again with name given":     {perms: "rlw", files: small, setup: goPublish(fixedID), args: append(append([]string{}, publish...), "--name", "Run 1"), normalize: fixed, unorderedStderr: true},
		"again with other content":  {perms: "rlw", files: small, setup: steps(goPublish(fixedID), changeFile("data/run1/a.txt", "A")), args: publish, normalize: fixed, unorderedStderr: true},
		"again with other size":     {perms: "rlw", files: small, setup: steps(goPublish(fixedID), changeFile("data/run1/a.txt", "AA")), args: publish, normalize: fixed, unorderedStderr: true},
		"again with extra file":     {perms: "rlw", files: small, setup: steps(goPublish(fixedID), changeFile("data/run1/extra.txt", "e")), args: publish, normalize: fixed, unorderedStderr: true},
		"again with file gone":      {perms: "rlw", files: small, setup: steps(goPublish(fixedID), func(t *testing.T, w *world) { os.Remove(filepath.Join(w.dir, "data", "run1", "c.txt")) }), args: publish, normalize: fixed, unorderedStderr: true},
		"again with stored differs": {perms: "rlw", files: small, setup: steps(goPublish(fixedID), func(t *testing.T, w *world) {
			putBlob(t, w, fixedID+"/run1/late.txt", "l", map[string]string{"sha256": sum256("l")})
		}), args: publish, normalize: fixed},
		"again with unreadable crate": {perms: "rlw", files: small, setup: func(t *testing.T, w *world) { putBlob(t, w, fixedID+"/"+crateName, "{", nil) }, args: publish, normalize: fixed},
		"again with crate of elsewhere": {perms: "rlw", files: small, setup: steps(goPublish(fixedID), func(t *testing.T, w *world) {
			c := readBlob(t, w, fixedID+"/"+crateName)
			putBlob(t, w, fixedID+"/"+crateName, strings.Replace(c, roCrateContext, "https://example.org/ctx", 1), nil)
		}), args: publish, normalize: fixed},
		"resume": {perms: "rlw", files: small, setup: attempt(small, "run1/a.txt", "run1/sub/b.csv"), args: publish, normalize: fixed, unorderedStderr: true},
		"resume where stored differs": {perms: "rlw", files: map[string]string{"data/run1/same.txt": "s", "data/run1/changed.csv": "run at 11:30", "data/run1/short.txt": "ab", "data/run1/new.txt": "n"},
			setup: func(t *testing.T, w *world) {
				for rel, body := range map[string]string{"same.txt": "s", "changed.csv": "run at 10:00", "gone.txt": "g", "short.txt": "abc"} {
					putBlob(t, w, fixedID+"/run1/"+rel, body, map[string]string{"sha256": sum256(body)})
				}
				putBlob(t, w, fixedID+"/run1/old.txt", "o", nil)
			}, args: publish, normalize: fixed, unorderedStderr: true},
		"read-only with write claimed": {perms: "rl", files: small, setup: func(t *testing.T, w *world) { setSASEnv(t, w, "AZURE_SAS_PERMISSIONS", "rlw") }, args: publish},
		"without list":                 {perms: "rw", files: small, args: publish},
		"missing container":            {ignoreBlobs: true, perms: "rlw", files: small, setup: func(t *testing.T, w *world) { adminContainer(t, w).Delete(context.Background(), nil) }, args: publish},
		"nothing listening":            {perms: "rlw", files: small, env: map[string]string{"LBF_BLOB_ENDPOINT": "http://127.0.0.1:1/devstoreaccount1"}, normalize: []*regexp.Regexp{reExpiry}, args: publish},
		"uploader not ascii":           {perms: "rlw", files: small, setup: func(t *testing.T, w *world) { setSASEnv(t, w, "USER_IDENTITY", "Zoë Example") }, args: publish, normalize: fixed, unorderedStderr: true},
		"uploader padded":              {perms: "rlw", files: small, setup: func(t *testing.T, w *world) { setSASEnv(t, w, "USER_IDENTITY", "  pad@example.org ") }, args: publish, normalize: fixed, unorderedStderr: true},
		"lost commit response":         {perms: "rlw", files: small, setup: func(t *testing.T, w *world) { proxyAzurite(t, w, onceWhen(isCommit)) }, args: publish, normalize: []*regexp.Regexp{reDone, reCount, rePort}, unorderedStderr: true},
		"lost crate response":          {perms: "rlw", files: small, setup: func(t *testing.T, w *world) { proxyAzurite(t, w, onceWhen(isCratePut)) }, args: publish, normalize: []*regexp.Regexp{reDone, reCount, rePort}, unorderedStderr: true},
		"another publish wrote the file": {perms: "rlw", files: map[string]string{"data/run1/a.txt": "a"}, setup: func(t *testing.T, w *world) {
			proxyAzurite(t, w, onceWhen(func(r *http.Request) bool {
				if isCommit(r) {
					putBlob(t, w, fixedID+"/run1/a.txt", "b", map[string]string{"sha256": sum256("b")})
				}
				return false
			}))
		}, args: publish, normalize: []*regexp.Regexp{reDone, reCount, rePort}},
		"another publish stored its crate": {perms: "rlw", files: map[string]string{"data/run1/a.txt": "a"}, setup: func(t *testing.T, w *world) {
			proxyAzurite(t, w, onceWhen(func(r *http.Request) bool {
				if isCommit(r) {
					putBlob(t, w, fixedID+"/"+crateName, lbfCrate(fixedID, roCrateContext, `{"@id": "./", "hasPart": {"@id": "run1/a.txt"}}`, `{"@id": "run1/a.txt", "contentSize": "1", "sha256": "`+sum256("a")+`"}`), nil)
				}
				return false
			}))
		}, args: publish, normalize: []*regexp.Regexp{reDone, reCount, rePort}},
		"server error then success": {perms: "rlw", files: small, setup: func(t *testing.T, w *world) {
			var n atomic.Int32
			proxyAzurite(t, w, func(r *http.Request) bool {
				return r.Method == http.MethodPut && r.URL.Query().Get("comp") == "block" && n.Add(1) <= 2
			})
		}, args: publish, normalize: []*regexp.Regexp{reDone, reCount, rePort}, unorderedStderr: true},
		"large file in blocks": {perms: "rlw", files: map[string]string{"data/run1/big.bin": bigFile(2*(8<<20) + 123), "data/run1/empty.txt": ""}, args: publish, normalize: fixed, unorderedStderr: true},
	}
	return cases
}

// Large and many-file datasets through both lbfs: publish, fetch, and how long each took.
func TestConformanceTransferVolume(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"40 MB file":      {"data/run1/big.bin": bigFile(40 << 20)},
		"500 small files": manyFiles(500),
	} {
		t.Run(name, func(t *testing.T) {
			norm := []*regexp.Regexp{reDone, reCount}
			c := conformCase{perms: "rlw", files: files, normalize: norm, unorderedStderr: true}
			timings := map[string]time.Duration{}
			c.args = []string{"publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID}
			c.setup = nil
			conformTimed(t, c, timings, "publish")
			c.setup = goPublish(fixedID)
			c.args = []string{"fetch", fixedID, "--sas-env", "{sas}", "--container", "{container}", "--out", "out"}
			conformTimed(t, c, timings, "fetch")
			t.Logf("%s: publish go %v ps %v; fetch go %v ps %v", name, timings["publish go"], timings["publish ps"], timings["fetch go"], timings["fetch ps"])
		})
	}
}

// conform, then the same command again in fresh worlds to time each lbf.
func conformTimed(t *testing.T, c conformCase, timings map[string]time.Duration, label string) {
	t.Helper()
	t.Run(label, func(t *testing.T) { conform(t, c) })
	timings[label+" go"], timings[label+" ps"] = timeBoth(t, c)
}

// Runs c's command once more in fresh worlds per lbf, returning the wall time of each.
func timeBoth(t *testing.T, c conformCase) (time.Duration, time.Duration) {
	t.Helper()
	var times [2]time.Duration
	for i, impl := range []string{"go", "ps"} {
		w := newWorld(t, impl, c.perms, c.files)
		if c.setup != nil {
			c.setup(t, w)
		}
		started := time.Now()
		var r lbfResult
		if impl == "go" {
			r = w.lbfGo(t, c.env, c.args...)
		} else {
			r = w.lbfPS(t, c.env, "", c.args...)
		}
		times[i] = time.Since(started)
		if r.code != 0 {
			t.Errorf("%s exited %d: %s", impl, r.code, r.stderr)
		}
	}
	return times[0], times[1]
}

func TestConformanceTransferProgressFunctions(t *testing.T) {
	ps := powershell(t)
	script, err := builtScript()
	must(t, err)
	var cmds strings.Builder
	fmt.Fprintf(&cmds, "$env:LBF_TEST_NO_MAIN = '1'; . '%s'\n", script)
	var want []string
	add := func(expr, value string) {
		fmt.Fprintf(&cmds, "[Console]::Out.Write((%s) + [char]0)\n", expr)
		want = append(want, value)
	}
	for _, n := range []int64{0, 1023, 1024, 1280, 1331, 1536, 1792, 10 << 20, 1<<30 + 1<<28, 5 << 40, 1<<53 - 1} {
		add(fmt.Sprintf("Format-LbfBytes %d", n), humanBytes(n))
	}
	for _, tc := range []struct {
		n, total int64
		width    int
	}{{0, 100, 8}, {50, 100, 8}, {100, 100, 8}, {0, 0, 8}, {33, 100, 3}, {1, 3, 40}} {
		add(fmt.Sprintf("Format-LbfBar %d %d %d", tc.n, tc.total, tc.width), bar(tc.n, tc.total, tc.width))
		add(fmt.Sprintf("Format-LbfCounts %d %d", tc.n, tc.total), counts(tc.n, tc.total))
	}
	for _, s := range []float64{0, 0.4, 0.5, 1.5, 42, 59.5, 303, 3599.5, 7620, 360000} {
		add(fmt.Sprintf("Format-LbfShortDuration %v", s), shortDuration(time.Duration(s*float64(time.Second))))
	}
	for _, ms := range []int64{0, 1, 999, 1000, 1234, 1200, 60000, 61500, 3600000, 3723004} {
		add(fmt.Sprintf("Format-LbfGoDuration %d", ms), (time.Duration(ms) * time.Millisecond).String())
	}
	for _, tc := range []struct {
		s string
		w int
	}{{"a.txt", 8}, {"run1/images/plate1.tiff", 12}, {"run1/细胞.tiff", 12}, {"run1/a.tiff", 2}, {"ab细胞", 4}, {"ab细胞", 5}, {"abc", -1}} {
		add(fmt.Sprintf("Format-LbfClipLeft '%s' %d", tc.s, tc.w), clipLeft(tc.s, tc.w))
		add(fmt.Sprintf("Format-LbfFit '%s' %d", tc.s, tc.w), fit(tc.s, tc.w))
	}
	add(`Format-LbfPrintable "bad`+"`t"+`name`+"`n"+`$([char]0x202e)gpj.exe"`, printable("bad\tname\n\u202egpj.exe"))

	// Whole frames at fixed sizes and a fixed rate, as Go's render draws them.
	names := []string{"a.txt", "run1/细胞图像/样本一.tiff", "tab\there\nnewline.bin", "\u202eevil.exe", "🧪🧫/" + strings.Repeat("x", 300) + ".parquet", "é́́.csv", "plate1/a.parquet", "b", "c", "d"}
	for _, size := range [][2]int{{1, 1}, {10, 2}, {40, 5}, {80, 24}, {57, 8}, {120, 60}, {200, 3}} {
		now := time.Now()
		p := &progress{live: true, size: func() (int, int) { return size[0], size[1] }, files: 40, total: 40 << 30,
			samples: []sample{{now.Add(-time.Minute), 0}}, done: 7, settled: 9 << 30}
		fmt.Fprintf(&cmds, "$p = New-LbfProgress -Files 40 -Total %d -Live $true -Width %d -Height %d; $now = [DateTime]::UtcNow; $p.Samples[0] = @($now.AddMinutes(-1), [long]0); $p.Done = 7; $p.Settled = [long]%d\n", int64(40<<30), size[0], size[1], int64(9<<30))
		for i, name := range names {
			b := p.start(name, int64(i+1)<<30)
			b.set(int64(i) << 29)
			fmt.Fprintf(&cmds, "$b = Start-LbfProgressBar $p ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))) %d; $b.N = [long]%d\n", b64(name), int64(i+1)<<30, int64(i)<<29)
		}
		for frame := range 2 {
			add("Format-LbfProgressFrame $p $now $false", p.render(now, false))
			_ = frame
		}
		p.end(p.active[0], true)
		fmt.Fprintf(&cmds, "Stop-LbfProgressBar $p $p.Active[0] $true\n")
		add("Format-LbfProgressFrame $p $now $true", p.render(now, true))
	}
	out, err := runPS(t, ps, cmds.String())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := strings.Split(out, "\x00")
	if len(got) != len(want)+1 {
		t.Fatalf("got %d values, want %d:\n%q", len(got)-1, len(want), out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("value %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// Every frame fits any terminal: lines no wider than a column short of the edge, no more than the height allows.
func TestConformanceTransferLiveProgressFits(t *testing.T) {
	ps := powershell(t)
	script, err := builtScript()
	must(t, err)
	names := []string{"a.txt", "run1/细胞图像/样本一.tiff", "tab\there\nnewline.bin", "\u202eevil.exe", "🧪🧫/" + strings.Repeat("x", 300) + ".parquet", "é́́.csv", "plate1/a.parquet", "b", "c", "d"}
	var cmds strings.Builder
	fmt.Fprintf(&cmds, "$env:LBF_TEST_NO_MAIN = '1'; . '%s'\n$names = @(", script)
	for i, n := range names {
		if i > 0 {
			cmds.WriteString(", ")
		}
		fmt.Fprintf(&cmds, "[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))", b64(n))
	}
	cmds.WriteString(`)
$esc = [regex]('\x1b\[\d*[A-Za-z]\r?')
foreach ($height in @(1, 2, 3, 5, 8, 24, 60)) {
  foreach ($width in (@(1..80) + @(97, 120, 159, 200))) {
    $p = New-LbfProgress -Files 40 -Total ([long]40 * 1GB) -Live $true -Width $width -Height $height
    $now = [DateTime]::UtcNow
    $p.Samples[0] = @($now.AddMinutes(-1), [long]0); $p.Done = 7; $p.Settled = [long]9 * 1GB
    for ($i = 0; $i -lt $names.Count; $i++) { $b = Start-LbfProgressBar $p $names[$i] ([long]($i + 1) * 1GB); $b.N = [long]$i * 512MB }
    foreach ($frame in 1..2) {
      $lines = $esc.Replace((Format-LbfProgressFrame $p $now $false), '') -split "` + "`n" + `"
      $lines = $lines[0..($lines.Count - 2)]
      if ($lines.Count -gt [Math]::Max($height - 2, 1)) { [Console]::Out.Write("${width}x${height}: $($lines.Count) lines` + "`n" + `") }
      foreach ($l in $lines) {
        if ([Text.Encoding]::UTF8.GetByteCount($l) -gt [Math]::Max($width - 1, 0) -or $l -match "[` + "`t`r" + `\u202e]") { [Console]::Out.Write("${width}x${height}: $l` + "`n" + `") }
      }
    }
  }
}
`)
	out, err := runPS(t, ps, cmds.String())
	if err != nil || out != "" {
		t.Fatalf("%v\n%s", err, out)
	}
}

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// Several PowerShell fetches of one dataset into one folder at once: each succeeds, and only the dataset is left.
func TestConformanceTransferConcurrentFetches(t *testing.T) {
	powershell(t)
	requireAzurite(t)
	w := newWorld(t, "ps", "rlw", manyFiles(40))
	goOK(t, w, "publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID)
	done := make(chan lbfResult, 3)
	for range 3 {
		go func() {
			done <- w.lbfPS(t, nil, "", "fetch", fixedID, "--sas-env", "{sas}", "--container", "{container}", "--out", "out")
		}()
	}
	for range 3 {
		r := <-done
		if r.code != 0 || strings.TrimSpace(w.normalize(r.stdout, nil)) != "{dir}/out/"+fixedID {
			t.Errorf("exit %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
		}
	}
	entries, err := os.ReadDir(filepath.Join(w.dir, "out"))
	must(t, err)
	if len(entries) != 1 || entries[0].Name() != fixedID {
		t.Errorf("out holds %v", entries)
	}
	if r := w.lbfGo(t, nil, "fetch", fixedID, "--sas-env", "{sas}", "--container", "{container}", "--out", "out"); r.code != 0 || !strings.Contains(r.stderr, "is already here and matches its crate") {
		t.Errorf("Go found the fetched folder wrong: exit %d\n%s", r.code, r.stderr)
	}
}

// Ctrl-C mid-transfer, through LBF_TEST_INTERRUPT: once Azure has acted on a matching request, lbf.ps1 stops as on Ctrl-C.
func TestConformanceTransferInterrupted(t *testing.T) {
	powershell(t)
	requireAzurite(t)
	small := map[string]string{"data/run1/a.txt": "a"}
	for _, c := range []struct {
		name, match, want string
		published         bool
	}{
		{"no crate yet", `^PUT .*run1%2Fa\.txt\?.*comp=blocklist`, "error: interrupted; " + fixedID + " was left without a crate, so fetch will refuse it. Run the same publish with --id " + fixedID + " to finish it\n", false},
		{"crate landed", `^PUT .*ro-crate-metadata\.json`, "error: interrupted, but " + fixedID + " was published; run the same publish with --id " + fixedID + " to check what landed\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, "ps", "rlw", small)
			r := w.lbfPS(t, map[string]string{"LBF_TEST_INTERRUPT": c.match}, "", "publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID)
			lines := strings.SplitAfter(strings.ReplaceAll(r.stderr, "\r\n", "\n"), "\n")
			if r.code != 1 || r.stdout != "" || len(lines) < 2 || lines[len(lines)-2] != c.want {
				t.Fatalf("exit %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
			}
			g := w.lbfGo(t, nil, "fetch", fixedID, "--sas-env", "{sas}", "--container", "{container}", "--out", "out")
			if (g.code == 0) != c.published {
				t.Fatalf("fetch afterwards: exit %d\n%s", g.code, g.stderr)
			}
			if again := w.lbfPS(t, nil, "", "publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID); again.code != 0 {
				t.Fatalf("publishing again: exit %d\n%s", again.code, again.stderr)
			}
		})
	}
	t.Run("fetch", func(t *testing.T) {
		w := newWorld(t, "ps", "rlw", manyFiles(20))
		goOK(t, w, "publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID)
		r := w.lbfPS(t, map[string]string{"LBF_TEST_INTERRUPT": `^GET .*f0005\.csv`}, "", "fetch", fixedID, "--sas-env", "{sas}", "--container", "{container}", "--out", "out")
		want := "error: interrupted; nothing was saved as " + filepath.Join("{dir}", "out", fixedID) + ", and the next fetch starts again\n"
		if r.code != 1 || !strings.HasSuffix(w.normalize(r.stderr, nil), want) {
			t.Fatalf("exit %d, stderr:\n%s", r.code, r.stderr)
		}
		if entries, _ := os.ReadDir(filepath.Join(w.dir, "out")); len(entries) != 0 {
			t.Fatalf("left %v in --out", entries)
		}
	})
}

// What Go reads from a crate (files, statement, differences from what is stored), as text both sides print alike.
func goCrateReading(raw string) string {
	c, err := readCrate([]byte(raw))
	if err != nil {
		return "error: " + err.Error()
	}
	var b strings.Builder
	for _, f := range c.Files {
		fmt.Fprintf(&b, "file %q %d %q\n", f.Rel, f.Size, f.SHA256)
	}
	stored := map[string]crateFile{crateName: {Size: 1}, "zz/extra": {Size: 2}}
	for i, f := range c.Files {
		if i%2 == 0 {
			stored[f.Rel] = crateFile{Rel: f.Rel, Size: f.Size + int64(i), SHA256: "x"}
		}
	}
	for _, p := range c.differences(stored, true) {
		fmt.Fprintf(&b, "problem %s\n", p)
	}
	p, conformsTo, err := c.statement()
	if err != nil {
		b.WriteString("statement error: " + err.Error() + "\n")
		return b.String()
	}
	j, _ := json.Marshal(p)
	sort.Strings(conformsTo)
	fmt.Fprintf(&b, "stated %s\nconformsTo %q\n", j, conformsTo)
	return b.String()
}

const psCrateReading = `
function Show-Crate([string]$b64) {
  $sb = New-Object System.Text.StringBuilder
  try { $c = Read-LbfStoredCrate ([Convert]::FromBase64String($b64)) } catch { return 'error: ' + (Get-LbfErrorText $_) }
  foreach ($f in $c.Files) { [void]$sb.Append("file $(Format-LbfQuote $f.Rel) $($f.Size) $(Format-LbfQuote $f.SHA256)` + "`n" + `") }
  $stored = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
  $stored['ro-crate-metadata.json'] = @{ Size = 1; SHA256 = '' }
  $stored['zz/extra'] = @{ Size = 2; SHA256 = '' }
  for ($i = 0; $i -lt $c.Files.Count; $i += 2) { $f = $c.Files[$i]; $stored[$f.Rel] = @{ Rel = $f.Rel; Size = $f.Size + $i; SHA256 = 'x' } }
  foreach ($p in (Get-LbfCrateDifferences $c $stored $true)) { [void]$sb.Append("problem $p` + "`n" + `") }
  try { $st = Get-LbfCrateStatement $c } catch { [void]$sb.Append("statement error: $(Get-LbfErrorText $_)` + "`n" + `"); return $sb.ToString() }
  $sorted = Get-LbfSortedUtf8 $st.ConformsTo.ToArray()
  $q = @(); foreach ($x in $sorted) { $q += Format-LbfQuote $x }
  [void]$sb.Append("stated $(ConvertTo-LbfJson (ConvertTo-LbfProvenanceValue $st.Provenance))` + "`n" + `conformsTo [$($q -join ' ')]` + "`n" + `")
  return $sb.ToString()
}
`

func TestConformanceTransferCrateReading(t *testing.T) {
	ps := powershell(t)
	script, err := builtScript()
	must(t, err)
	root := func(extra string) string {
		return `{"@id": "./", "@type": "Dataset", "identifier": "` + fixedID + `", "hasPart": [{"@id": "run1/a%20b%231%25.csv"}, {"@id": "run1/old.txt"}]` + extra + `}`
	}
	files := `{"@id": "run1/a%20b%231%25.csv", "@type": "File", "contentSize": "5", "sha256": "abc"}, {"@id": "run1/old.txt", "@type": "File", "contentSize": 3}`
	ctx := `"@context": "` + roCrateContext + `"`
	crate := func(rootExtra string, entities ...string) string {
		return `{` + ctx + `, "@graph": [` + root(rootExtra) + `, ` + files + strings.Join(append([]string{""}, entities...), ", ") + `]}`
	}
	pv := func(id, name, value string) string {
		return `{"@id": "` + id + `", "@type": "PropertyValue", "name": ` + name + `, "value": ` + value + `}`
	}
	sw := func(id, name string) string {
		return `{"@id": "` + id + `", "@type": "SoftwareApplication", "name": "` + name + `", "version": "1", "url": "` + id + `"}`
	}
	crates := map[string]string{
		"earlier lbf":          earlierLbfCrate,
		"plain":                crate(""),
		"name and description": crate(`, "name": "Run 1", "description": "Cells"`),
		"name as a list":       crate(`, "name": ["Run 1"], "description": ["a", "b"]`),
		"name as a value":      crate(`, "name": {"@value": "Run 1"}, "description": 7`),
		"default name":         crate(`, "name": "run1", "description": "Dataset of 2 files from run1", "additionalProperty": {"@id": "#source_path"}`, pv("#source_path", `"source_path"`, `"/data/run1"`)),
		"windows source":       crate(`, "name": "run1", "description": "Dataset of 2 files from run1", "additionalProperty": {"@id": "#source_path"}`, pv("#source_path", `"source_path"`, `"C:/data/run1"`)),
		"old descriptions":     crate(`, "name": "` + fixedID + `", "description": "Bronze-layer dataset ` + fixedID + `"`),
		"properties": crate(`, "additionalProperty": [{"@id": "#a"}, {"@id": "#a"}, {"@id": "#b"}, {"@id": "#c1"}, {"@id": "#c2"}, {"@id": "#missing"}, {"@id": "#noname"}, {"@id": "#num"}, {"@id": "#subscription_id"}]`,
			pv("#a", `"a"`, `"1"`), pv("#b", `"b b"`, `["2"]`), pv("#c1", `"c"`, `"3"`), pv("#c2", `"c"`, `"4"`), `{"@id": "#noname", "value": "5"}`, pv("#num", `"num"`, `6`), pv("#subscription_id", `"subscription_id"`, `"s"`)),
		"parents": crate(`, "isBasedOn": [{"@id": "#source-x"}, {"@id": "#source-y"}], "wasDerivedFrom": {"@id": "#source-z"}`,
			`{"@id": "#source-x", "identifier": "x"}`, `{"@id": "#source-y", "identifier": "y"}`, `{"@id": "#source-z", "identifier": "z"}`),
		"parent not in graph": crate(`, "isBasedOn": {"@id": "#source-q"}`),
		"parent as text":      crate(`, "isBasedOn": "20260101-a-b-0000"`),
		"runs": crate(`, "mentions": {"@id": "#run"}`, sw("https://a.example/x", "A"), sw("https://b.example/y", "B"),
			`{"@id": "#run", "@type": "CreateAction", "instrument": [{"@id": "https://b.example/y"}, {"@id": "https://a.example/x"}, {"@id": "https://b.example/y"}, {"@id": "https://gone.example"}], "result": {"@id": "./"}}`,
			`{"@id": "#other", "@type": "CreateAction", "instrument": {"@id": "https://a.example/x"}, "result": [{"@id": "#elsewhere"}]}`),
		"conformsTo":        crate(`, "conformsTo": [{"@id": "https://example.org/b"}, {"@id": "https://example.org/a"}, {"@id": "https://example.org/a"}, "text", "text", {"@id": "#p"}]`, `{"@id": "#p", "name": "P"}`),
		"context in a list": strings.Replace(crate(""), ctx, `"@context": ["`+roCrateContext+`", {"@vocab": "http://schema.org/"}]`, 1),
		"1.2 context":       strings.Replace(crate(`, "name": "Run 1"`), ctx, `"@context": "`+roCrate12Context+`"`, 1),
		"no context":        strings.Replace(crate(`, "name": "Run 1"`), ctx, `"@context": null`, 1),
		"other context":     strings.Replace(crate(""), ctx, `"@context": ["`+roCrateContext+`", "https://example.org/extra"]`, 1),
		"not a file entity": `{` + ctx + `, "@graph": [{"@id": "./", "hasPart": [{"@id": "run1/a"}, {"@id": "#x"}]}, {"@id": "run1/a", "@type": ["File"], "contentSize": "1"}, {"@id": "#x", "@type": "Dataset", "contentSize": "-2", "name": "x"}]}`,
		"sizes":             `{"@graph": [{"@id": "./", "hasPart": [{"@id": "a"}, {"@id": "b"}, {"@id": "c"}, {"@id": "d"}]}, {"@id": "a", "contentSize": "+7"}, {"@id": "b", "contentSize": 999999}, {"@id": "c", "contentSize": -5.0}, {"@id": "d", "contentSize": "0"}]}`,
		"size too big":      `{"@graph": [{"@id": "./", "hasPart": {"@id": "a"}}, {"@id": "a", "contentSize": "9223372036854775808"}]}`,
		"size fraction":     `{"@graph": [{"@id": "./", "hasPart": {"@id": "a"}}, {"@id": "a", "contentSize": 1.5}]}`,
		"duplicate ids":     `{"@graph": [{"@id": "./", "hasPart": {"@id": "a"}}, {"@id": "a", "contentSize": "1"}, {"@id": "a", "contentSize": "2"}]}`,
		"duplicate keys":    `{"@graph": [{"@id": "./", "hasPart": {"@id": "a"}}, {"@id": "a", "contentSize": "1", "contentSize": "3"}], "@GRAPH": null}`,
		"folded key":        `{"@Graph": [{"@id": "./", "hasPart": {"@id": "a"}}, {"@id": "a", "contentSize": "1"}]}`,
		"no root":           `{"@graph": [{"@id": "a", "contentSize": "1"}]}`,
		"null graph":        `{"@graph": null}`,
		"null entity":       `{"@graph": [null, {"@id": "./", "hasPart": {"@id": "a"}}, {"@id": "a", "contentSize": "1"}]}`,
		"empty id":          `{"@graph": [{"@id": "./", "hasPart": {"@id": ""}}, {"@id": "", "contentSize": "1"}]}`,
		"number":            `5`,
		"string":            `"x"`,
		"bad graph entry":   `{"@graph": [{"@id": "./"}, 5]}`,
	}
	var names []string
	for n := range crates {
		names = append(names, n)
	}
	sort.Strings(names)
	var cmds strings.Builder
	fmt.Fprintf(&cmds, "$env:LBF_TEST_NO_MAIN = '1'; . '%s'\n%s\n", script, psCrateReading)
	for _, n := range names {
		fmt.Fprintf(&cmds, "[Console]::Out.Write((Show-Crate '%s') + [char]0)\n", b64(crates[n]))
	}
	out, err := runPS(t, ps, cmds.String())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := strings.Split(out, "\x00")
	if len(got) != len(names)+1 {
		t.Fatalf("got %d results for %d crates:\n%s", len(got)-1, len(names), out)
	}
	for i, n := range names {
		if want := goCrateReading(crates[n]); got[i] != want {
			t.Errorf("%s:\n%s", n, lineDiff(want, got[i]))
		}
	}
}

// Go's TestTransferStopsStartingAfterAFailure: once a file fails, no more are started.
func TestConformanceTransferStopsAfterAFailure(t *testing.T) {
	ps := powershell(t)
	script, err := builtScript()
	must(t, err)
	out, err := runPS(t, ps, fmt.Sprintf(`$env:LBF_TEST_NO_MAIN = '1'; . '%s'
$script:calls = 0
function Start-LbfJob { param($Job) $script:calls++; throw 'boom' }
$jobs = New-Object System.Collections.Generic.List[hashtable]
for ($i = 0; $i -lt 100; $i++) { $jobs.Add((New-LbfJob 'put' "f$i" 0)) }
try { Invoke-LbfTransferAll (New-LbfProgress 100 0 -Discard) $jobs; 'no error' } catch { "$($_.Exception.Message) after $script:calls" }`, script))
	if err != nil || strings.TrimSpace(out) != "boom after 8" {
		t.Fatalf("%v: %q", err, out)
	}
}
