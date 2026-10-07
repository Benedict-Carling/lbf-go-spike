package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
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
