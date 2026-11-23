//go:build unix

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestUpgradeOnAFullDiskCleansUp(t *testing.T) {
	if os.Getenv("LBF_FULL_DISK_HELPER") == "" {
		// RLIMIT_FSIZE is per process, so lowering it here would also fail the test runner's own writes.
		cmd := exec.Command(os.Args[0], "-test.run=^TestUpgradeOnAFullDiskCleansUp$", "-test.v")
		cmd.Env = append(os.Environ(), "LBF_FULL_DISK_HELPER=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v:\n%s", err, out)
		}
		if strings.Contains(string(out), "--- SKIP") {
			t.Skipf("%s", out)
		}
		return
	}
	bin := []byte(strings.Repeat("new lbf ", 1024))
	sum := sha256.Sum256(bin)
	srv := fakeReleases(t, "v0.2.0", bin, hex.EncodeToString(sum[:]))
	exe := filepath.Join(t.TempDir(), "lbf")
	writeFile(t, exe, "old lbf")

	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &lim); err != nil {
		t.Skip(err)
	}
	small := lim
	small.Cur = 1024
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &small); err != nil {
		t.Skip(err)
	}
	_, _, err := upgrade(context.Background(), srv.URL+"/releases", "v0.1.5", exe)
	syscall.Setrlimit(syscall.RLIMIT_FSIZE, &lim)

	if err == nil || strings.Contains(err.Error(), "own folder") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatalf("left %s.new behind: %v", exe, err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old lbf" {
		t.Fatalf("exe holds %q", got)
	}
}
