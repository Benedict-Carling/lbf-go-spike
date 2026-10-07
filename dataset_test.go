package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSymlinkedRootIsNamedAfterTheLink(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "annex", "SHA256E-s4--abcdef.csv"), "data")
	writeFile(t, filepath.Join(base, "store", "job-9f3a", "x.txt"), "x")
	if os.Symlink(filepath.Join("annex", "SHA256E-s4--abcdef.csv"), filepath.Join(base, "results.csv")) != nil {
		t.Skip("cannot create symlinks here")
	}
	must(t, os.Symlink(filepath.Join("store", "job-9f3a"), filepath.Join(base, "results")))

	source, files, err := datasetFiles(filepath.Join(base, "results.csv"))
	if err != nil || rels(files) != "results.csv" || filepath.Base(source) != "results.csv" {
		t.Fatalf("file link: %s %s %v", source, rels(files), err)
	}
	if body, _ := os.ReadFile(files[0].Path); string(body) != "data" {
		t.Fatalf("file link reads %q", body)
	}
	source, files, err = datasetFiles(filepath.Join(base, "results"))
	if err != nil || rels(files) != "results/x.txt" || filepath.Base(source) != "results" {
		t.Fatalf("folder link: %s %s %v", source, rels(files), err)
	}
	pub := publication{ID: newID(), Source: source, Files: files}
	if got := pub.view(target{}, time.Now(), nil)["data"].(map[string]any)["files"].([]map[string]any)[0]["path"]; got != "x.txt" {
		t.Fatalf("profile sees %v", got)
	}

	t.Chdir(filepath.Join(base, "store"))
	for _, input := range []string{"job-9f3a", "job-9f3a/", "./job-9f3a/.", "job-9f3a" + string(filepath.Separator)} {
		if _, files, err := datasetFiles(input); err != nil || rels(files) != "job-9f3a/x.txt" {
			t.Errorf("%q: %s %v", input, rels(files), err)
		}
	}
	t.Chdir(filepath.Join(base, "store", "job-9f3a"))
	if _, files, err := datasetFiles("."); err != nil || rels(files) != "job-9f3a/x.txt" {
		t.Errorf(".: %s %v", rels(files), err)
	}
}

func TestFoldersStartingWithDotUnderscoreAreKept(t *testing.T) {
	root := filepath.Join(t.TempDir(), "run1")
	for _, p := range []string{"._results/a.txt", "._b.txt", "sub/.DS_Store", ".DS_Store/c.txt"} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(p)), "x")
	}
	_, files, err := datasetFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := rels(files); got != "run1/.DS_Store/c.txt,run1/._results/a.txt" {
		t.Fatalf("got %s", got)
	}
}

func TestSingleInputMustBeARegularFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/null or mkfifo")
	}
	inputs := []string{"/dev/null"}
	if _, err := exec.LookPath("mkfifo"); err == nil {
		fifo := filepath.Join(t.TempDir(), "pipe")
		must(t, exec.Command("mkfifo", fifo).Run())
		inputs = append(inputs, fifo)
	}
	for _, input := range inputs {
		done := make(chan error, 1)
		go func() {
			_, _, err := datasetFiles(input)
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "not a regular file") {
				t.Errorf("%s: %v", input, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: datasetFiles hung", input)
		}
	}
}
