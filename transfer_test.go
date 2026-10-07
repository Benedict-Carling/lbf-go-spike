package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func crateOf(t *testing.T, tg target, id string) ([]crateFile, []byte) {
	t.Helper()
	cc, err := tg.client()
	must(t, err)
	crate, err := downloadBuffer(context.Background(), cc, id+"/"+crateName)
	must(t, err)
	files, err := crateFiles(crate)
	must(t, err)
	return files, crate
}

func TestConcurrentFetchesOfOneIDIntoOneFolder(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	files := map[string]string{}
	for i := range 100 {
		files[fmt.Sprintf("f%03d.bin", i)] = fmt.Sprint(i)
	}
	pub := prepared(t, dataset(t, files), "", provenanceFlags{})
	must(t, upload(ctx, tg, pub))
	listed, crate := crateOf(t, tg, pub.ID)

	for attempt := range 10 {
		out := t.TempDir()
		root := filepath.Join(out, pub.ID)
		var wg sync.WaitGroup
		for fetcher := range 3 {
			wg.Go(func() {
				got, err := download(ctx, tg, pub.ID, out)
				if err != nil {
					t.Errorf("attempt %d, fetch %d: %v", attempt, fetcher, err)
					return
				}
				if problem, err := verifyDir(got.path, listed, crate); problem != "" || err != nil {
					t.Errorf("attempt %d, fetch %d succeeded, but %s is not the dataset: %s %v", attempt, fetcher, root, problem, err)
				}
			})
		}
		wg.Wait()
		if entries, _ := os.ReadDir(out); len(entries) != 1 {
			t.Errorf("attempt %d left %d entries in --out, not just %s", attempt, len(entries), pub.ID)
		}
		if t.Failed() {
			return
		}
	}
}

type fetcherKey struct{}

func TestFetchNeverReportsAnotherFetchsUnfinishedFolder(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	files := map[string]string{}
	for i := range 20 {
		files[fmt.Sprintf("f%02d.bin", i)] = fmt.Sprint(i)
	}
	pub := prepared(t, dataset(t, files), "", provenanceFlags{})
	must(t, upload(ctx, tg, pub))
	listed, crate := crateOf(t, tg, pub.ID)

	aRenaming, releaseA := make(chan struct{}), make(chan struct{})
	bMidway, releaseB := make(chan struct{}), make(chan struct{})
	var bOnce sync.Once
	defer func(h func(context.Context, string)) { fetchHook = h }(fetchHook)
	fetchHook = func(ctx context.Context, stage string) {
		switch {
		case ctx.Value(fetcherKey{}) == "A" && stage == "renaming":
			close(aRenaming)
			<-releaseA
		case ctx.Value(fetcherKey{}) == "B" && stage == "downloaded":
			bOnce.Do(func() {
				close(bMidway)
				<-releaseB
			})
		}
	}

	out := t.TempDir()
	type result struct {
		got fetched
		err error
	}
	fetch := func(name string) chan result {
		done := make(chan result, 1)
		go func() {
			got, err := download(context.WithValue(ctx, fetcherKey{}, name), tg, pub.ID, out)
			done <- result{got, err}
		}()
		return done
	}
	a := fetch("A")
	<-aRenaming
	b := fetch("B")
	<-bMidway
	close(releaseA)
	ra := <-a
	if ra.err != nil {
		t.Fatalf("A: %v", ra.err)
	}
	if problem, err := verifyDir(ra.got.path, listed, crate); problem != "" || err != nil {
		t.Errorf("A reported success while %s was not the dataset: %s %v", ra.got.path, problem, err)
	}
	close(releaseB)
	rb := <-b
	if rb.err != nil {
		t.Fatalf("B: %v", rb.err)
	}
	if problem, err := verifyDir(rb.got.path, listed, crate); problem != "" || err != nil {
		t.Errorf("B reported success while %s was not the dataset: %s %v", rb.got.path, problem, err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 1 {
		t.Errorf("left %d entries in --out, not just %s", len(entries), pub.ID)
	}
}

// Reaches Azurite through a proxy that drops the response to each request lost picks once Azurite has acted on it.
func lossy(t *testing.T, tg target, lost func(*http.Request) bool) target {
	t.Helper()
	backend, err := url.Parse("http://127.0.0.1:10000")
	must(t, err)
	forward := httputil.NewSingleHostReverseProxy(backend)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := httptest.NewRecorder()
		forward.ServeHTTP(resp, r)
		if lost(r) {
			if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		maps.Copy(w.Header(), resp.Header())
		w.WriteHeader(resp.Code)
		w.Write(resp.Body.Bytes())
	}))
	t.Cleanup(srv.Close)
	tg.endpoint = srv.URL + "/" + emulatorAccount
	return tg
}

func once(match func(*http.Request) bool) func(*http.Request) bool {
	var done atomic.Bool
	return func(r *http.Request) bool {
		return match(r) && done.CompareAndSwap(false, true)
	}
}

func TestPublishSurvivesALostCommitResponse(t *testing.T) {
	for name, commit := range map[string]func(*http.Request) bool{
		"file": func(r *http.Request) bool {
			return r.Method == http.MethodPut && r.URL.Query().Get("comp") == "blocklist"
		},
		"crate": func(r *http.Request) bool {
			return r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/"+crateName)
		},
	} {
		t.Run(name, func(t *testing.T) {
			tg := emulator(t, uploadPerms)
			ctx := context.Background()
			pub := prepared(t, dataset(t, map[string]string{"a.txt": "a"}), newID(), provenanceFlags{})
			if err := upload(ctx, lossy(t, tg, once(commit)), pub); err != nil {
				t.Fatalf("a commit that landed was reported as failed: %v", err)
			}
			got, err := download(ctx, tg, pub.ID, t.TempDir())
			must(t, err)
			body, err := os.ReadFile(filepath.Join(got.dataPath, "a.txt"))
			must(t, err)
			if string(body) != "a" {
				t.Fatalf("fetched %q", body)
			}
		})
	}
}

func TestUploadingIdenticalBytesTwiceIsNotAnotherPublish(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	cc, err := tg.client()
	must(t, err)
	id := newID()
	for range 2 {
		pub := prepared(t, dataset(t, map[string]string{"a.txt": "a"}), id, provenanceFlags{})
		if err := uploadFile(ctx, cc, id+"/run1/a.txt", &pub.Files[0], tg.User, func(int64) {}); err != nil {
			t.Fatal(err)
		}
		if len(pub.Files[0].SHA256) != 64 {
			t.Fatalf("sha256 not recorded: %q", pub.Files[0].SHA256)
		}
	}
}

func TestAnotherPublishsCrateLandingFirstIsNotCalledMissing(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	cc, err := tg.client()
	must(t, err)
	dir := dataset(t, map[string]string{"a.txt": "a"})
	id := newID()
	other := prepared(t, dir, id, provenanceFlags{})
	other.Files[0].SHA256 = "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
	_, conformsTo, err := other.validate(tg)
	must(t, err)

	beatIt := func(r *http.Request) bool {
		if r.Method == http.MethodPut && r.URL.Query().Get("comp") == "blocklist" {
			must(t, uploadCrate(ctx, cc, tg, other, time.Now().Add(-time.Minute), conformsTo))
		}
		return false
	}
	err = upload(ctx, lossy(t, tg, beatIt), prepared(t, dir, id, provenanceFlags{}))
	if err == nil || strings.Contains(err.Error(), "without a crate") || !strings.Contains(err.Error(), "another publish of the same ID stored its crate first") {
		t.Fatalf("got %v", err)
	}
	if _, ok := errors.AsType[idTaken](err); ok {
		t.Fatalf("refused with exit code 3, though the other publish may have stored the same dataset: %v", err)
	}
	must(t, upload(ctx, tg, prepared(t, dir, id, provenanceFlags{})))
}

func TestPublishAgainChecksWhatIsStoredAgainstTheCrate(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	cc, err := tg.client()
	must(t, err)
	id := newID()
	dir := dataset(t, map[string]string{"a.txt": "a"})
	must(t, upload(ctx, tg, prepared(t, dir, id, provenanceFlags{})))
	late := prepared(t, dataset(t, map[string]string{"a.txt": "a", "b.txt": "b"}), id, provenanceFlags{})
	must(t, uploadFile(ctx, cc, id+"/run1/b.txt", &late.Files[1], tg.User, func(int64) {}))
	if _, err := download(ctx, tg, id, t.TempDir()); err == nil {
		t.Fatal("fetch accepted a dataset with a file its crate does not list")
	}

	err = upload(ctx, tg, prepared(t, dir, id, provenanceFlags{}))
	if _, ok := errors.AsType[idTaken](err); !ok || !strings.Contains(err.Error(), "run1/b.txt: stored but not listed in the crate") || !strings.Contains(err.Error(), "fetch will refuse it") {
		t.Fatalf("rerun of a publish that fetch refuses: %v", err)
	}
}
