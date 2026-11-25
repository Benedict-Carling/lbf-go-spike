package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lbf's saved login lives under the user config directory; keep tests away from the real one.
func isolateHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("AppData", filepath.Join(dir, "AppData"))
	old := tokenCache
	t.Cleanup(func() { tokenCache = old })
	tokenCache = hermeticTokenCache
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	must(t, err)
	defer f.Close()
	old := os.Stderr
	os.Stderr = f
	defer func() { os.Stderr = old }()
	fn()
	raw, err := os.ReadFile(f.Name())
	must(t, err)
	return string(raw)
}

type refuseAll struct{}

func (refuseAll) Do(req *http.Request) (*http.Response, error) {
	return nil, errors.New("tests never reach Entra")
}

func withoutBrowser(t *testing.T) {
	t.Helper()
	oldReason, oldTransport := noBrowser, authTransport
	noBrowser = func() string { return "stdin is not a terminal" }
	authTransport = refuseAll{}
	t.Cleanup(func() { noBrowser, authTransport = oldReason, oldTransport })
}

func TestLoginFailsFastWithoutABrowser(t *testing.T) {
	isolateHome(t)
	withoutBrowser(t)
	_, err := login(context.Background(), imperialTenant)
	if err == nil {
		t.Fatal("login succeeded without a browser")
	}
	for _, want := range []string{"stdin is not a terminal", "az login --tenant " + imperialTenant, "--use-device-code", "--sas-env", "lbf mint-sas"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestNoBrowserReasons(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	for _, c := range []struct {
		name          string
		stdin, stderr bool
		goos          string
		env           map[string]string
		want          string
	}{
		{"desktop linux", true, true, "linux", map[string]string{"DISPLAY": ":0"}, ""},
		{"wayland", true, true, "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, ""},
		{"ssh to linux", true, true, "linux", nil, "DISPLAY"},
		{"batch job", false, true, "linux", map[string]string{"DISPLAY": ":0"}, "stdin"},
		{"stderr redirected", true, false, "darwin", nil, "stderr"},
		{"mac terminal", true, true, "darwin", nil, ""},
		{"windows terminal", true, true, "windows", nil, ""},
	} {
		if got := browserUnavailable(c.stdin, c.stderr, c.goos, env(c.env)); (got == "") != (c.want == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want it to mention %q", c.name, got, c.want)
		}
	}
}

func TestSASWindowToleratesAFastClock(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	start, expiry := sasWindow(now)
	if !start.Equal(now.Add(-15 * time.Minute)) {
		t.Errorf("start %s, want 15 minutes before %s", start, now)
	}
	if !expiry.Equal(now.Add(sasLifetime)) {
		t.Errorf("expiry %s, want %s after now", expiry, sasLifetime)
	}
	if expiry.Sub(start) > 7*24*time.Hour {
		t.Errorf("a delegation key cannot last %s", expiry.Sub(start))
	}
}

func TestCheckSaysWhichSignInRatherThanAThrowawayExpiry(t *testing.T) {
	expiry := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	minted := target{Account: "acct", Container: "bronze", User: "u@ic.ac.uk", Expiry: expiry, Source: "az login"}
	got := checkSummary(minted, "upload")
	if strings.Contains(got, "2026-10-13") || !strings.Contains(got, "az login") {
		t.Errorf("signed-in check: %q", got)
	}
	fromFile := target{Account: "acct", Container: "bronze", User: "u@ic.ac.uk", Expiry: expiry}
	if got := checkSummary(fromFile, "upload"); !strings.Contains(got, "until 2026-10-13T12:00:00Z") {
		t.Errorf("--sas-env check: %q", got)
	}
}

func TestUnusableKeychainIsNotBlamedOnTheBuild(t *testing.T) {
	locked := errors.New("the macOS keychain is not available: User interaction is not allowed")
	for _, msg := range []string{loginMessage("u@ic.ac.uk", locked), logoutMessage("", false, false, locked)} {
		if strings.Contains(msg, "this build") || strings.Contains(msg, "This build") {
			t.Errorf("blames the build: %q", msg)
		}
	}
	if msg := loginMessage("u@ic.ac.uk", locked); !strings.Contains(msg, "User interaction is not allowed") {
		t.Errorf("login hides why: %q", msg)
	}
	if msg := logoutMessage("", false, false, errNoCredentialStore); !strings.Contains(msg, "never saves a login") {
		t.Errorf("static build logout: %q", msg)
	}
	if msg := loginMessage("u@ic.ac.uk", nil); !strings.Contains(msg, "until 'lbf logout'") {
		t.Errorf("remembered login: %q", msg)
	}
}
