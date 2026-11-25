package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/mattn/go-isatty"
	"github.com/pkg/browser"
	"golang.org/x/term"
)

const (
	imperialTenant = "2b897507-ee8c-4575-830b-4f8267c3d307"
	// The Azure CLI's public client ID: Imperial's sign-in policies already allow it.
	azureCLIClientID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"
	tokenCacheName   = "lbf"
	azTimeout        = 2 * time.Minute
	azNotSignedIn    = "Please run 'az login'"
)

var armScope = policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}}

var errNoCredentialStore = errors.New("this build of lbf cannot remember logins")

var (
	noBrowser = func() string {
		return browserUnavailable(isTerminal(os.Stdin), isTerminal(os.Stderr), runtime.GOOS, os.Getenv)
	}
	loginTimeout  = 5 * time.Minute
	authTransport policy.Transporter
)

func credential(ctx context.Context, tenant string) (azcore.TokenCredential, string, error) {
	if rec, ok := loadRecord(); ok {
		cred, err := savedLogin(ctx, tenant, rec)
		if err == nil {
			return cred, "lbf login", nil
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		logf("[auth] cannot use the login 'lbf login' saved for %s: %v\n", cmp.Or(rec.Username, "you"), err)
	}

	if _, err := exec.LookPath("az"); err == nil {
		cli, err := newAzCLI(tenant)
		if err != nil {
			return nil, "", err
		}
		_, err = cli.GetToken(ctx, armScope)
		switch {
		case err == nil:
			return cli, "az login", nil
		case ctx.Err() != nil:
			return nil, "", ctx.Err()
		case !strings.Contains(err.Error(), azNotSignedIn):
			return nil, "", fmt.Errorf("could not sign in through az login: %w\nIf that login has expired or is for another tenant, run 'az login --tenant %s' ('az login --tenant %s --use-device-code' over SSH or on HPC)", err, tenant, tenant)
		}
	}

	if err := browserCheck(tenant); err != nil {
		return nil, "", err
	}
	logf("[auth] not signed in; opening your browser to sign in\n")
	cred, err := login(ctx, tenant)
	return cred, "browser", err
}

func savedLogin(ctx context.Context, tenant string, rec azidentity.AuthenticationRecord) (azcore.TokenCredential, error) {
	if _, err := tokenCache(); err != nil {
		return nil, err
	}
	probe, err := browserCredential(tenant, rec, true)
	if err != nil {
		return nil, err
	}
	if _, err := probe.GetToken(ctx, armScope); err != nil {
		return nil, err
	}
	// Later scopes may still need the browser if the cached refresh token cannot serve them.
	return browserCredential(tenant, rec, false)
}

func login(ctx context.Context, tenant string) (azcore.TokenCredential, error) {
	if err := browserCheck(tenant); err != nil {
		return nil, err
	}
	cred, err := browserCredential(tenant, azidentity.AuthenticationRecord{}, false)
	if err != nil {
		return nil, err
	}
	// pkg/browser passes the launcher its stdout, which carries lbf's results.
	browser.Stdout, browser.Stderr = os.Stderr, os.Stderr
	wait, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	rec, err := cred.Authenticate(wait, &armScope)
	if err != nil && ctx.Err() == nil && wait.Err() != nil {
		return nil, fmt.Errorf("browser sign-in timed out after %s; if no browser opened, sign in with 'az login --tenant %s --use-device-code' instead", loginTimeout, tenant)
	}
	if err != nil {
		return nil, fmt.Errorf("browser sign-in failed: %w", err)
	}
	if _, err := tokenCache(); err == nil {
		if err := saveRecord(rec); err != nil {
			return nil, err
		}
	}
	return cred, nil
}

func browserCheck(tenant string) error {
	if reason := noBrowser(); reason != "" {
		return fmt.Errorf("cannot sign in with a browser here: %s\nSign in with 'az login --tenant %s' first ('az login --tenant %s --use-device-code' works over SSH and on HPC), or use --sas-env with a file from 'lbf mint-sas' run on a machine with a browser", reason, tenant, tenant)
	}
	return nil
}

func browserUnavailable(stdinTerminal, stderrTerminal bool, goos string, getenv func(string) string) string {
	switch {
	case !stdinTerminal:
		return "stdin is not a terminal, as in a batch job"
	case !stderrTerminal:
		return "stderr is not a terminal"
	case goos == "linux" && getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "":
		return "there is no display (neither DISPLAY nor WAYLAND_DISPLAY is set)"
	}
	return ""
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd())) || isatty.IsCygwinTerminal(f.Fd())
}

// The SDK gives each az call 10 seconds and runs az again for every client and scope.
type azCLI struct {
	cred   *azidentity.AzureCLICredential
	mu     sync.Mutex
	tokens map[string]azcore.AccessToken
}

func newAzCLI(tenant string) (*azCLI, error) {
	cred, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{TenantID: tenant})
	if err != nil {
		return nil, err
	}
	return &azCLI{cred: cred, tokens: map[string]azcore.AccessToken{}}, nil
}

func (a *azCLI) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	key := opts.TenantID + " " + strings.Join(opts.Scopes, " ")
	a.mu.Lock()
	defer a.mu.Unlock()
	if tok, ok := a.tokens[key]; ok && opts.Claims == "" && time.Until(tok.ExpiresOn) > 5*time.Minute {
		return tok, nil
	}
	wait, cancel := context.WithTimeout(ctx, azTimeout)
	defer cancel()
	tok, err := a.cred.GetToken(wait, opts)
	if err != nil && ctx.Err() == nil && wait.Err() != nil {
		return tok, fmt.Errorf("az took longer than %s to return a token", azTimeout)
	}
	if err != nil {
		return tok, err
	}
	a.tokens[key] = tok
	return tok, nil
}

func logout() (user string, hadRecord, hadTokens bool, err error) {
	path, err := recordPath()
	if err != nil {
		return "", false, false, err
	}
	rec, _ := loadRecord()
	hadTokens, err = clearTokenCache(tokenCacheName)
	if err != nil {
		return rec.Username, false, hadTokens, fmt.Errorf("could not delete lbf's saved tokens, so you are still signed in: %w", err)
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return rec.Username, false, hadTokens, nil
	}
	return rec.Username, true, hadTokens, err
}

func azCLIUser(ctx context.Context, tenant string) (string, error) {
	cli, err := newAzCLI(tenant)
	if err != nil {
		return "", err
	}
	return signedInUser(ctx, cli)
}

func browserCredential(tenant string, rec azidentity.AuthenticationRecord, silentOnly bool) (*azidentity.InteractiveBrowserCredential, error) {
	c, _ := tokenCache()
	return azidentity.NewInteractiveBrowserCredential(&azidentity.InteractiveBrowserCredentialOptions{
		ClientOptions:                  azcore.ClientOptions{Transport: authTransport},
		ClientID:                       azureCLIClientID,
		TenantID:                       tenant,
		Cache:                          c,
		AuthenticationRecord:           rec,
		DisableAutomaticAuthentication: silentOnly,
	})
}

func recordPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lbf", "auth-record.json"), nil
}

// The record only names the account; the tokens themselves live in the OS credential store.
func loadRecord() (azidentity.AuthenticationRecord, bool) {
	var rec azidentity.AuthenticationRecord
	path, err := recordPath()
	if err != nil {
		return rec, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return rec, false
	}
	return rec, json.Unmarshal(raw, &rec) == nil
}

func saveRecord(rec azidentity.AuthenticationRecord) error {
	path, err := recordPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}
