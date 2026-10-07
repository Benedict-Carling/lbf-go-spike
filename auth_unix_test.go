//go:build !windows

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// An az on PATH that runs script and counts its calls.
func fakeAz(t *testing.T, script string) func() int {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	writeFile(t, filepath.Join(dir, "az"), "#!/bin/sh\necho >> '"+calls+"'\n"+script)
	must(t, os.Chmod(filepath.Join(dir, "az"), 0o755))
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return func() int {
		raw, _ := os.ReadFile(calls)
		return strings.Count(string(raw), "\n")
	}
}

func azPrintsToken(user string) string {
	claims, _ := json.Marshal(map[string]string{"upn": user})
	jwt := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	return fmt.Sprintf("cat <<'EOF'\n{\"accessToken\": %q, \"expires_on\": %d}\nEOF\n", jwt, time.Now().Add(time.Hour).Unix())
}

func TestSlowAzLoginIsStillUsed(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the Azure SDK's 10s limit on az")
	}
	isolateHome(t)
	withoutBrowser(t)
	fakeAz(t, "sleep 11\n"+azPrintsToken("az@ic.ac.uk"))
	_, source, err := credential(context.Background(), imperialTenant)
	if err != nil || source != "az login" {
		t.Fatalf("got %q, %v; want az login", source, err)
	}
}

func TestAzTokensAreReusedPerScope(t *testing.T) {
	isolateHome(t)
	withoutBrowser(t)
	calls := fakeAz(t, azPrintsToken("az@ic.ac.uk"))
	ctx := context.Background()
	cred, _, err := credential(ctx, imperialTenant)
	must(t, err)
	user, err := signedInUser(ctx, cred)
	must(t, err)
	if user != "az@ic.ac.uk" {
		t.Fatalf("user %q", user)
	}
	storage := policy.TokenRequestOptions{Scopes: []string{"https://storage.azure.com/.default"}}
	for range 3 {
		_, err := cred.GetToken(ctx, storage)
		must(t, err)
	}
	if n := calls(); n != 2 {
		t.Fatalf("az ran %d times for two scopes", n)
	}
}

func TestAzErrorIsShownInsteadOfOpeningTheBrowser(t *testing.T) {
	isolateHome(t)
	withoutBrowser(t)
	fakeAz(t, "echo 'ERROR: AADSTS700082: The refresh token has expired due to inactivity.' >&2\nexit 1\n")
	_, _, err := credential(context.Background(), imperialTenant)
	if err == nil || !strings.Contains(err.Error(), "AADSTS700082") || !strings.Contains(err.Error(), "az login --tenant "+imperialTenant) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "stdin is not a terminal") {
		t.Fatalf("fell through to the browser: %v", err)
	}
}

func TestWithoutAzLoginTheBrowserIsNext(t *testing.T) {
	isolateHome(t)
	withoutBrowser(t)
	for name, script := range map[string]string{
		"not signed in": "echo \"ERROR: Please run 'az login' to setup account.\" >&2\nexit 1\n",
		"not installed": "",
	} {
		calls := fakeAz(t, script)
		if script == "" {
			t.Setenv("PATH", t.TempDir())
		}
		_, _, err := credential(context.Background(), imperialTenant)
		if err == nil || !strings.Contains(err.Error(), "stdin is not a terminal") {
			t.Errorf("%s: got %v, want the browser's refusal", name, err)
		}
		if script == "" && calls() != 0 {
			t.Errorf("%s: az ran", name)
		}
	}
}

func TestUnusableSavedLoginIsReported(t *testing.T) {
	isolateHome(t)
	withoutBrowser(t)
	must(t, saveRecord(azidentity.AuthenticationRecord{Username: "saved@ic.ac.uk", TenantID: imperialTenant, ClientID: azureCLIClientID, Authority: "login.microsoftonline.com", HomeAccountID: "a.b", Version: "1.0"}))
	old := tokenCache
	tokenCache = func() (azidentity.Cache, error) {
		return azidentity.Cache{}, fmt.Errorf("the macOS keychain is not available: User interaction is not allowed")
	}
	t.Cleanup(func() { tokenCache = old })
	fakeAz(t, azPrintsToken("other@ic.ac.uk"))

	var source string
	var err error
	logged := captureStderr(t, func() { _, source, err = credential(context.Background(), imperialTenant) })
	if err != nil || source != "az login" {
		t.Fatalf("got %q, %v", source, err)
	}
	for _, want := range []string{"saved@ic.ac.uk", "User interaction is not allowed"} {
		if !strings.Contains(logged, want) {
			t.Errorf("log lacks %q:\n%s", want, logged)
		}
	}
}

// Answers Entra's discovery requests, so an interactive sign-in gets as far as the browser.
type fakeAuthority struct{}

func (fakeAuthority) Do(req *http.Request) (*http.Response, error) {
	tenant := strings.Split(req.URL.Path, "/")[1]
	body := fmt.Sprintf(`{"token_endpoint": "https://%[1]s/%[2]s/oauth2/v2.0/token", "issuer": "https://%[1]s/%[2]s/v2.0", "authorization_endpoint": "https://%[1]s/%[2]s/oauth2/v2.0/authorize"}`, req.URL.Host, tenant)
	if strings.Contains(req.URL.Path, "discovery/instance") {
		body = fmt.Sprintf(`{"tenant_discovery_endpoint": "https://%[1]s/%[2]s/v2.0/.well-known/openid-configuration", "metadata": [{"preferred_network": "%[1]s", "preferred_cache": "%[1]s", "aliases": ["%[1]s"]}]}`, req.URL.Host, imperialTenant)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func TestBrowserSignInTimesOutAndKeepsStdoutClean(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	opened := filepath.Join(dir, "opened")
	for _, launcher := range []string{"open", "xdg-open"} {
		writeFile(t, filepath.Join(dir, launcher), "#!/bin/sh\ntouch '"+opened+"'\necho launcher-noise\n")
		must(t, os.Chmod(filepath.Join(dir, launcher), 0o755))
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	oldReason, oldTransport, oldTimeout := noBrowser, authTransport, loginTimeout
	noBrowser, authTransport, loginTimeout = func() string { return "" }, fakeAuthority{}, 2*time.Second
	t.Cleanup(func() { noBrowser, authTransport, loginTimeout = oldReason, oldTransport, oldTimeout })

	out, err := os.CreateTemp(t.TempDir(), "stdout")
	must(t, err)
	oldStdout := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = oldStdout }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	began := time.Now()
	var loginErr error
	logged := captureStderr(t, func() { _, loginErr = login(ctx, imperialTenant) })
	os.Stdout = oldStdout

	if _, err := os.Stat(opened); err != nil {
		t.Fatalf("the browser was never opened: %v", loginErr)
	}
	if loginErr == nil || !strings.Contains(loginErr.Error(), "timed out") || time.Since(began) > 10*time.Second {
		t.Fatalf("after %s: %v", time.Since(began).Round(time.Second), loginErr)
	}
	stdout, err := os.ReadFile(out.Name())
	must(t, err)
	if strings.Contains(string(stdout), "launcher-noise") || !strings.Contains(logged, "launcher-noise") {
		t.Fatalf("launcher output: stdout %q, stderr %q", stdout, logged)
	}
}
