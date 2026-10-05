package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	releasesURL      = "https://github.com/Benedict-Carling/lbf-go-spike/releases"
	updateCheckEvery = time.Hour
	updateCheckLimit = 2 * time.Second
)

// startUpdateCheck looks up the latest release while the command runs; the returned func prints a warning if lbf is out of date.
func startUpdateCheck(ctx context.Context, args []string) func() {
	if version == "dev" || os.Getenv("LBF_NO_UPDATE_CHECK") != "" || (len(args) > 0 && args[0] == "upgrade") {
		return func() {}
	}
	done := make(chan string, 1)
	go func() {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			done <- ""
			return
		}
		done <- cachedLatestVersion(ctx, releasesURL, filepath.Join(cacheDir, "lbf", "latest-version"))
	}()
	return func() {
		if latest := <-done; newerVersion(latest, version) {
			fmt.Fprint(os.Stderr, upgradeWarning(version, latest))
		}
	}
}

func upgradeWarning(current, latest string) string {
	rule := strings.Repeat("=", 64)
	return fmt.Sprintf("\n%s\n  lbf %s is out of date: the latest release is %s.\n  Please upgrade now by running:  lbf upgrade\n%s\n", rule, current, latest, rule)
}

// cachedLatestVersion asks GitHub at most once per updateCheckEvery, failures included, so an offline machine is not slowed on every run.
func cachedLatestVersion(ctx context.Context, base, cache string) string {
	cached, _ := os.ReadFile(cache)
	if info, err := os.Stat(cache); err == nil && time.Since(info.ModTime()) < updateCheckEvery {
		return string(cached)
	}
	ctx, cancel := context.WithTimeout(ctx, updateCheckLimit)
	defer cancel()
	if latest, err := latestVersion(ctx, base); err == nil {
		cached = []byte(latest)
	}
	_ = os.MkdirAll(filepath.Dir(cache), 0o755)
	_ = os.WriteFile(cache, cached, 0o644)
	return string(cached)
}

// latestVersion reads the tag from the releases/latest redirect, which avoids the GitHub API's rate limit.
func latestVersion(ctx context.Context, base string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, base+"/latest", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("checking for the latest lbf release: %w", err)
	}
	resp.Body.Close()
	_, tag, ok := strings.Cut(resp.Header.Get("Location"), "/releases/tag/")
	if !ok || tag == "" {
		return "", fmt.Errorf("no lbf release found at %s (HTTP %d)", base, resp.StatusCode)
	}
	return tag, nil
}

func parseVersion(v string) ([]int, bool) {
	p := make([]int, 3)
	_, err := fmt.Sscanf(v, "v%d.%d.%d", &p[0], &p[1], &p[2])
	return p, err == nil
}

func newerVersion(latest, current string) bool {
	l, okL := parseVersion(latest)
	c, okC := parseVersion(current)
	return okL && okC && slices.Compare(l, c) > 0
}

func assetName() string {
	name := "lbf-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// upgrade replaces exe with the latest release if it is newer than current, after checking it against the release's SHA256SUMS.
func upgrade(ctx context.Context, base, current, exe string) (latest string, upgraded bool, err error) {
	latest, err = latestVersion(ctx, base)
	if err != nil || !newerVersion(latest, current) {
		return latest, false, err
	}
	download := base + "/download/" + latest + "/"
	sums, err := httpGet(ctx, download+"SHA256SUMS")
	if err != nil {
		return latest, false, err
	}
	want, err := checksumFor(sums, assetName())
	if err != nil {
		return latest, false, err
	}
	bin, err := httpGet(ctx, download+assetName())
	if err != nil {
		return latest, false, err
	}
	if got := sha256.Sum256(bin); hex.EncodeToString(got[:]) != want {
		return latest, false, fmt.Errorf("downloaded %s does not match its published checksum; nothing was changed", assetName())
	}
	if err := replaceExecutable(exe, bin); err != nil {
		return latest, false, fmt.Errorf("replacing %s: %w", exe, err)
	}
	return latest, true, nil
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func checksumFor(sums []byte, name string) (string, error) {
	s := bufio.NewScanner(bytes.NewReader(sums))
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS has no entry for %s", name)
}

func replaceExecutable(exe string, bin []byte) error {
	tmp := exe + ".new"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return fmt.Errorf("%w; lbf cannot write to its own folder, so ask whoever installed it to upgrade, or install your own copy with the command in the README", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Rename(tmp, exe); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return nil
	}
	// Windows will not overwrite a running exe, but it will rename one.
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w; an lbf started before the last upgrade may still be running: close it and run 'lbf upgrade' again", err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		return errors.Join(err, os.Rename(old, exe))
	}
	return nil
}
