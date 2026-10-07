package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatasetFilesFindsJunctionLoops(t *testing.T) {
	root := filepath.Join(t.TempDir(), "run1")
	writeFile(t, filepath.Join(root, "a.txt"), "a")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "loop"), root).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v %s", err, out)
	}
	_, _, err := datasetFiles(root)
	if err == nil || !strings.Contains(err.Error(), "run1/loop: symlink loop") || strings.Contains(err.Error(), "loop/loop") {
		t.Fatalf("got %v", err)
	}
}
