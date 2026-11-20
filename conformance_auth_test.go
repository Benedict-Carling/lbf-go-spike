package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
)

const (
	fakeUser   = "researcher@ic.ac.uk"
	fakeOID    = "6b3c3d1e-0000-4000-8000-000000000001"
	fakeSubA   = "aaaaaaaa-0000-4000-8000-00000000000a"
	fakeSubB   = "bbbbbbbb-0000-4000-8000-00000000000b"
	fakeSubOff = "dddddddd-0000-4000-8000-00000000000d"
)

var (
	signInScope = "https://management.azure.com/.default openid offline_access profile"
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func fakeJWT(claims map[string]any) string {
	raw, _ := json.Marshal(claims)
	return "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." + base64.RawURLEncoding.EncodeToString(raw) + ".c2ln"
}

func accessToken(resource, user string) string {
	return fakeJWT(map[string]any{"aud": resource, "upn": user, "tid": imperialTenant, "oid": fakeOID})
}

type fakeFailure struct {
	status int
	header map[string]string
	body   string
}

// Entra, ARM and the blob service, as lbf.ps1 sees them through LBF_TEST_AUTHORITY, LBF_TEST_ARM and LBF_TEST_BLOB_SERVICE.
type fakeAzure struct {
	t   *testing.T
	srv *httptest.Server

	mu            sync.Mutex
	codes         map[string][3]string // code -> challenge, redirect, scope
	refresh       map[string]bool
	tokenFailure  *fakeFailure
	armFailures   map[string]fakeFailure
	keyFailures   map[string]fakeFailure
	grants        []string
	blobOps       []string
	problems      []string
	refreshIssued int
}

func newFakeAzure(t *testing.T) *fakeAzure {
	f := &fakeAzure{t: t, codes: map[string][3]string{}, refresh: map[string]bool{}, armFailures: map[string]fakeFailure{}, keyFailures: map[string]fakeFailure{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() {
		f.srv.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, p := range f.problems {
			t.Error(p)
		}
	})
	return f
}

func (f *fakeAzure) problem(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.problems = append(f.problems, fmt.Sprintf(format, args...))
}

func (f *fakeAzure) env(urlFile string) map[string]string {
	return map[string]string{
		"LBF_TEST_AUTHORITY":    f.srv.URL + "/entra",
		"LBF_TEST_ARM":          f.srv.URL + "/arm",
		"LBF_TEST_BLOB_SERVICE": f.srv.URL + "/blob/{account}",
		"LBF_TEST_BROWSER":      urlFile,
	}
}

func writeFailure(w http.ResponseWriter, fail fakeFailure) {
	for k, v := range fail.header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(fail.status)
	io.WriteString(w, fail.body)
}

func (f *fakeAzure) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/entra/"):
		f.serveEntra(w, r)
	case strings.HasPrefix(r.URL.Path, "/arm/"):
		f.serveARM(w, r)
	case strings.HasPrefix(r.URL.Path, "/blob/"):
		f.serveBlob(w, r)
	default:
		f.problem("unexpected request %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

func (f *fakeAzure) serveEntra(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/entra/"), "/", 2)
	tenant, rest := parts[0], parts[1]
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch {
	case rest == "v2.0/.well-known/openid-configuration" && r.Method == http.MethodGet:
		if tenant == "nosuchtenant" {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_tenant","error_description":"AADSTS90002: Tenant 'nosuchtenant' not found.","error_codes":[90002],"error_uri":"https://login.microsoftonline.com/error?code=90002"}`)
			return
		}
		base := f.srv.URL + "/entra/" + tenant
		fmt.Fprintf(w, `{"authorization_endpoint":%q,"token_endpoint":%q,"issuer":"https://login.microsoftonline.com/%s/v2.0"}`, base+"/oauth2/v2.0/authorize", base+"/oauth2/v2.0/token", tenant)
	case rest == "oauth2/v2.0/token" && r.Method == http.MethodPost:
		f.serveToken(w, r, tenant)
	default:
		f.problem("unexpected Entra request %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

func (f *fakeAzure) serveToken(w http.ResponseWriter, r *http.Request, tenant string) {
	must(f.t, r.ParseForm())
	form := r.PostForm
	invalid := func(desc string) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"invalid_grant","error_description":%q,"error_codes":[700082]}`, desc)
	}
	if form.Get("client_id") != azureCLIClientID || form.Get("client_info") != "1" {
		f.problem("token request with client_id %q client_info %q", form.Get("client_id"), form.Get("client_info"))
	}
	f.mu.Lock()
	f.grants = append(f.grants, form.Get("grant_type")+" "+form.Get("scope"))
	fail := f.tokenFailure
	f.mu.Unlock()
	resource := ""
	if scopes := strings.Fields(form.Get("scope")); len(scopes) > 0 {
		resource = strings.TrimSuffix(scopes[0], "/.default")
	}
	switch form.Get("grant_type") {
	case "authorization_code":
		if fail != nil {
			writeFailure(w, *fail)
			return
		}
		f.mu.Lock()
		issued, ok := f.codes[form.Get("code")]
		delete(f.codes, form.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		switch {
		case !ok:
			invalid("AADSTS70008: unknown code")
			return
		case base64.RawURLEncoding.EncodeToString(sum[:]) != issued[0]:
			f.problem("code_verifier does not match code_challenge")
			invalid("AADSTS501481: The Code_Verifier does not match the code_challenge supplied in the authorization request.")
			return
		case form.Get("redirect_uri") != issued[1] || form.Get("scope") != issued[2]:
			f.problem("token request redirect_uri %q scope %q; authorize had %q %q", form.Get("redirect_uri"), form.Get("scope"), issued[1], issued[2])
		}
	case "refresh_token":
		f.mu.Lock()
		ok := f.refresh[form.Get("refresh_token")]
		delete(f.refresh, form.Get("refresh_token"))
		f.mu.Unlock()
		if !ok {
			invalid("AADSTS700082: The refresh token has expired due to inactivity.")
			return
		}
		if !strings.HasSuffix(form.Get("scope"), " openid offline_access profile") {
			f.problem("refresh for scope %q", form.Get("scope"))
		}
	default:
		f.problem("grant_type %q", form.Get("grant_type"))
		invalid("unsupported grant")
		return
	}
	f.mu.Lock()
	f.refreshIssued++
	rt := fmt.Sprintf("rt-%d", f.refreshIssued)
	f.refresh[rt] = true
	f.mu.Unlock()
	info, _ := json.Marshal(map[string]string{"uid": fakeOID, "utid": imperialTenant})
	id := fakeJWT(map[string]any{"aud": azureCLIClientID, "iss": "https://login.microsoftonline.com/" + imperialTenant + "/v2.0", "tid": imperialTenant, "oid": fakeOID, "preferred_username": fakeUser, "name": "Researcher"})
	json.NewEncoder(w).Encode(map[string]any{
		"token_type": "Bearer", "scope": resource + "/user_impersonation", "expires_in": 4799, "ext_expires_in": 4799,
		"access_token": accessToken(resource, fakeUser), "refresh_token": rt, "id_token": id,
		"client_info": base64.RawURLEncoding.EncodeToString(info),
	})
}

func bearerAudience(r *http.Request) string {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct{ Aud string }
	json.Unmarshal(raw, &claims)
	return claims.Aud
}

func (f *fakeAzure) serveARM(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/arm")
	if aud := bearerAudience(r); aud != "https://management.azure.com" {
		f.problem("ARM request %s with a token for %q", r.URL, aud)
	}
	f.mu.Lock()
	fail, failing := f.armFailures[path]
	f.mu.Unlock()
	if failing {
		writeFailure(w, fail)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	page2 := r.URL.Query().Get("$skiptoken") == "2"
	next := func() string {
		q := r.URL.Query()
		q.Set("$skiptoken", "2")
		return f.srv.URL + r.URL.Path + "?" + q.Encode()
	}
	sub := func(id, name, state string) map[string]any {
		return map[string]any{"id": "/subscriptions/" + id, "subscriptionId": id, "displayName": name, "state": state, "tenantId": imperialTenant}
	}
	acct := func(name, location string, tags map[string]any) map[string]any {
		return map[string]any{"name": name, "location": location, "tags": tags, "kind": "StorageV2"}
	}
	var page map[string]any
	switch path {
	case "/subscriptions":
		if r.URL.Query().Get("api-version") != "2022-12-01" {
			f.problem("subscriptions api-version %q", r.URL.Query().Get("api-version"))
		}
		if page2 {
			page = map[string]any{"value": []any{sub(fakeSubB, "Sub B", "Enabled")}}
		} else {
			page = map[string]any{"value": []any{sub(fakeSubA, "Sub A", "Enabled"), sub(fakeSubOff, "Old Sub", "Disabled")}, "nextLink": next()}
		}
	case "/subscriptions/" + fakeSubA + "/providers/Microsoft.Storage/storageAccounts":
		if r.URL.Query().Get("api-version") != "2024-01-01" {
			f.problem("storage accounts api-version %q", r.URL.Query().Get("api-version"))
		}
		if page2 {
			page = map[string]any{"value": []any{acct("lbfa", "ukwest", map[string]any{"tag": "storage-test", "owner": nil})}}
		} else {
			page = map[string]any{"value": []any{
				acct("lbfb", "uksouth", map[string]any{"tag": "storage-test"}),
				acct("untagged", "uksouth", nil),
				acct("lbfprod", "uksouth", map[string]any{"tag": "storage", "Tag": "storage-test"}),
			}, "nextLink": next()}
		}
	case "/subscriptions/" + fakeSubB + "/providers/Microsoft.Storage/storageAccounts":
		page = map[string]any{"value": []any{acct("lbfc", "uksouth", map[string]any{"tag": "storage-test"})}}
	default:
		f.problem("unexpected ARM request %s", r.URL)
		http.NotFound(w, r)
		return
	}
	json.NewEncoder(w).Encode(page)
}

func (f *fakeAzure) delegationKey(account string) []byte {
	sum := sha256.Sum256([]byte("delegation key for " + account))
	return sum[:]
}

func (f *fakeAzure) serveBlob(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/blob/")
	account, path, _ := strings.Cut(rest, "/")
	q := r.URL.Query()
	if q.Get("comp") == "userdelegationkey" {
		f.serveDelegationKey(w, r, account)
		return
	}
	container, blob, _ := strings.Cut(path, "/")
	f.mu.Lock()
	f.blobOps = append(f.blobOps, r.Method+" "+path+" "+q.Get("comp"))
	f.mu.Unlock()
	if err := f.checkSAS(q, account, container); err != nil {
		f.problem("blob request %s %s: %v", r.Method, r.URL, err)
		w.Header().Set("x-ms-error-code", "AuthenticationFailed")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	switch {
	case r.Method == http.MethodGet && blob == "" && q.Get("comp") == "list":
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, "\ufeff<?xml version=\"1.0\" encoding=\"utf-8\"?><EnumerationResults ServiceEndpoint=\"%s/\" ContainerName=\"%s\"><MaxResults>1</MaxResults><Blobs /><NextMarker /></EnumerationResults>", f.srv.URL, container)
	case r.Method == http.MethodPut && q.Get("comp") == "block":
		if !strings.Contains(q.Get("sp"), "w") {
			w.Header().Set("x-ms-error-code", "AuthorizationPermissionMismatch")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
	default:
		f.problem("unexpected blob request %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

func (f *fakeAzure) serveDelegationKey(w http.ResponseWriter, r *http.Request, account string) {
	if r.Method != http.MethodPost || r.URL.Query().Get("restype") != "service" {
		f.problem("delegation key request %s %s", r.Method, r.URL)
	}
	if aud := bearerAudience(r); aud != "https://storage.azure.com" {
		f.problem("delegation key request with a token for %q", aud)
	}
	if v := r.Header.Get("x-ms-version"); v != "2026-12-06" {
		f.problem("delegation key request with x-ms-version %q", v)
	}
	f.mu.Lock()
	fail, failing := f.keyFailures[account]
	f.mu.Unlock()
	if failing {
		writeFailure(w, fail)
		return
	}
	var info struct{ Start, Expiry string }
	if err := xml.NewDecoder(r.Body).Decode(&info); err != nil || info.Start == "" || info.Expiry == "" {
		f.problem("delegation key request body: %v %+v", err, info)
	}
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprintf(w, "\ufeff<?xml version=\"1.0\" encoding=\"utf-8\"?><UserDelegationKey><SignedOid>%s</SignedOid><SignedTid>%s</SignedTid><SignedStart>%s</SignedStart><SignedExpiry>%s</SignedExpiry><SignedService>b</SignedService><SignedVersion>2026-12-06</SignedVersion><Value>%s</Value></UserDelegationKey>",
		fakeOID, imperialTenant, info.Start, info.Expiry, base64.StdEncoding.EncodeToString(f.delegationKey(account)))
}

// Recomputes the signature over the string-to-sign azblob's SignWithUserDelegation builds for a container.
func (f *fakeAzure) checkSAS(q url.Values, account, container string) error {
	sts := strings.Join([]string{
		q.Get("sp"), q.Get("st"), q.Get("se"), "/blob/" + account + "/" + container,
		q.Get("skoid"), q.Get("sktid"), q.Get("skt"), q.Get("ske"), q.Get("sks"), q.Get("skv"),
		"", "", "", q.Get("skdutid"), "", "", q.Get("spr"), q.Get("sv"), q.Get("sr"),
		"", "", "", "", "", "", "", "", "",
	}, "\n")
	mac := hmac.New(sha256.New, f.delegationKey(account))
	mac.Write([]byte(sts))
	if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); q.Get("sig") != want {
		return fmt.Errorf("signature %q, want %q", q.Get("sig"), want)
	}
	if q.Get("skoid") != fakeOID || q.Get("sr") != "c" || q.Get("spr") != "https" || q.Get("sv") != "2026-12-06" {
		return fmt.Errorf("unexpected SAS fields %v", q)
	}
	return nil
}

type fakeStorageCredential struct{}

func (fakeStorageCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: accessToken("https://storage.azure.com", fakeUser), ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// Sends the Go SDK's requests for https://<account>.blob.core.windows.net to the fake.
type toFakeBlob struct {
	f       *fakeAzure
	account string
}

func (t toFakeBlob) Do(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.f.srv.URL + "/blob/" + t.account + req.URL.Path)
	u.RawQuery = req.URL.RawQuery
	out := req.Clone(req.Context())
	out.URL, out.Host, out.RequestURI = u, u.Host, ""
	return http.DefaultClient.Do(out)
}

// The SAS the Go lbf's mintTarget would encode for the same key window, signed by the Go SDK.
func (f *fakeAzure) goSAS(t *testing.T, account, container, perms string, start, expiry time.Time) string {
	t.Helper()
	svc, err := service.NewClient("https://"+account+".blob.core.windows.net/", fakeStorageCredential{}, &service.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: toFakeBlob{f, account}}})
	must(t, err)
	udc, err := svc.GetUserDelegationCredential(context.Background(), service.KeyInfo{Start: new(start.Format(sas.TimeFormat)), Expiry: new(expiry.Format(sas.TimeFormat))}, nil)
	must(t, err)
	qp, err := sas.BlobSignatureValues{Protocol: sas.ProtocolHTTPS, StartTime: start, ExpiryTime: expiry, Permissions: perms, ContainerName: container}.SignWithUserDelegation(udc)
	must(t, err)
	return qp.Encode()
}

// Checks the sign-in URL lbf.ps1 opened carries what MSAL Go's AuthCodeURL sends, then posts the redirect a browser would.
func (f *fakeAzure) approve(t *testing.T, signIn *url.URL, tenant string, reply func(state string) url.Values) (int, string) {
	t.Helper()
	q := signIn.Query()
	if signIn.Path != "/entra/"+tenant+"/oauth2/v2.0/authorize" {
		t.Errorf("sign-in path %q", signIn.Path)
	}
	var keys []string
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if got := strings.Join(keys, ","); got != "client_id,code_challenge,code_challenge_method,prompt,redirect_uri,response_mode,response_type,scope,state" {
		t.Errorf("sign-in parameters %s", got)
	}
	if signIn.RawQuery != q.Encode() {
		t.Errorf("sign-in query is not encoded as Go's url.Values would:\n%s\n%s", signIn.RawQuery, q.Encode())
	}
	for k, want := range map[string]string{"client_id": azureCLIClientID, "response_type": "code", "response_mode": "form_post", "prompt": "select_account", "code_challenge_method": "S256", "scope": signInScope} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if !regexp.MustCompile(`^http://localhost:[0-9]+$`).MatchString(q.Get("redirect_uri")) || len(q.Get("code_challenge")) != 43 || !uuidPattern.MatchString(q.Get("state")) {
		t.Errorf("redirect_uri %q, code_challenge %q, state %q", q.Get("redirect_uri"), q.Get("code_challenge"), q.Get("state"))
	}
	form := url.Values{}
	if reply != nil {
		form = reply(q.Get("state"))
	} else {
		code := fmt.Sprintf("code-%d", time.Now().UnixNano())
		f.mu.Lock()
		f.codes[code] = [3]string{q.Get("code_challenge"), q.Get("redirect_uri"), q.Get("scope")}
		f.mu.Unlock()
		form.Set("code", code)
		form.Set("state", q.Get("state"))
		form.Set("session_state", "00000000-0000-0000-0000-000000000000")
	}
	resp, err := http.PostForm(q.Get("redirect_uri"), form)
	if err != nil {
		t.Fatalf("posting the redirect: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// Runs lbf.ps1 in w while playing the browser for each sign-in URL it writes to LBF_TEST_BROWSER.
func (f *fakeAzure) runPS(t *testing.T, w *world, env map[string]string, browser func(*url.URL), args ...string) lbfResult {
	t.Helper()
	urlFile := filepath.Join(w.home, "sign-in-url")
	// A URL left by an earlier command in this world is not this command's sign-in.
	if err := os.Remove(urlFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	full := f.env(urlFile)
	full["PATH"] = noAzPath(t)
	for k, v := range env {
		full[k] = v
	}
	done := make(chan lbfResult, 1)
	go func() { done <- w.lbfPS(t, full, "", args...) }()
	last := ""
	deadline := time.After(4 * time.Minute)
	for {
		select {
		case r := <-done:
			r.stdout = w.normalize(r.stdout, nil)
			r.stderr = w.normalize(r.stderr, nil)
			return r
		case <-deadline:
			t.Fatal("lbf.ps1 did not finish")
		case <-time.After(50 * time.Millisecond):
		}
		raw, err := os.ReadFile(urlFile)
		if err != nil || len(raw) == 0 || string(raw) == last {
			continue
		}
		last = string(raw)
		u, err := url.Parse(last)
		must(t, err)
		if browser == nil {
			t.Errorf("unexpected sign-in: %s", last)
			continue
		}
		browser(u)
	}
}

func (f *fakeAzure) approveAll(t *testing.T, tenant string) func(*url.URL) {
	return func(u *url.URL) {
		status, body := f.approve(t, u, tenant, nil)
		if status != http.StatusOK || !strings.Contains(body, "Authentication complete. You can return to the application.") {
			t.Errorf("sign-in page %d:\n%s", status, body)
		}
	}
}

// A PATH with no az on it; Windows keeps System32 for cmd.exe.
func noAzPath(t *testing.T) string {
	return pathWith(t.TempDir())
}

func pathWith(dir string) string {
	if runtime.GOOS == "windows" {
		return dir + string(os.PathListSeparator) + filepath.Join(os.Getenv("SystemRoot"), "System32")
	}
	return dir
}

// A directory holding a fake az (and az.cmd) that behaves as named, and the file it logs its arguments to.
func fakeAzDir(t *testing.T, behaviour string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	tokens := map[string]string{}
	for _, res := range []string{"https://management.azure.com", "https://storage.azure.com"} {
		p := filepath.Join(dir, strings.TrimPrefix(strings.Split(res, ".")[0], "https://")+".json")
		writeFile(t, p, fmt.Sprintf(`{"accessToken": %q, "expires_on": %d, "tokenType": "Bearer"}`, accessToken(res, "az@ic.ac.uk"), time.Now().Add(time.Hour).Unix()))
		tokens[res] = p
	}
	sh := map[string]string{
		"token":      fmt.Sprintf("case \"$*\" in *storage.azure.com*) /bin/cat '%s' ;; *) /bin/cat '%s' ;; esac\n", tokens["https://storage.azure.com"], tokens["https://management.azure.com"]),
		"expired":    "echo 'ERROR: AADSTS700082: The refresh token has expired due to inactivity.' >&2\nexit 1\n",
		"signed-out": "echo \"ERROR: Please run 'az login' to setup account.\" >&2\nexit 1\n",
		"garbage":    "echo 'not json'\n",
		"silent":     "exit 3\n",
	}
	cmd := map[string]string{
		"token":      fmt.Sprintf("echo %%* | findstr /c:\"storage.azure.com\" >nul && (type \"%s\") || (type \"%s\")\r\n", tokens["https://storage.azure.com"], tokens["https://management.azure.com"]),
		"expired":    ">&2 echo ERROR: AADSTS700082: The refresh token has expired due to inactivity.\r\nexit /b 1\r\n",
		"signed-out": ">&2 echo ERROR: Please run 'az login' to setup account.\r\nexit /b 1\r\n",
		"garbage":    "echo not json\r\n",
		"silent":     "exit /b 3\r\n",
	}
	writeFile(t, filepath.Join(dir, "az"), "#!/bin/sh\necho \"$@\" >> '"+calls+"'\n"+sh[behaviour])
	must(t, os.Chmod(filepath.Join(dir, "az"), 0o755))
	writeFile(t, filepath.Join(dir, "az.cmd"), "@echo off\r\necho %*>> \""+calls+"\"\r\n"+cmd[behaviour])
	return dir, calls
}

func configDir(w *world) string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(w.home, "AppData", "Roaming")
	case "darwin":
		return filepath.Join(w.home, "Library", "Application Support")
	}
	return filepath.Join(w.home, ".config")
}

func saveTestRecord(t *testing.T, w *world) {
	raw, err := json.Marshal(azidentity.AuthenticationRecord{Authority: "https://login.microsoftonline.com", ClientID: azureCLIClientID, HomeAccountID: "a.b", TenantID: imperialTenant, Username: "saved@ic.ac.uk", Version: "1.0"})
	must(t, err)
	writeFile(t, filepath.Join(configDir(w), "lbf", "auth-record.json"), string(raw))
}

// The cgo Go lbf on macOS keeps logins in the keychain, which the PowerShell lbf cannot, and logout would delete the real one.
func skipWhereStoresDiffer(t *testing.T) {
	if runtime.GOOS != "darwin" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "CGO_ENABLED" && s.Value == "0" {
				return
			}
		}
	}
	t.Skip("the Go lbf built with cgo uses the macOS keychain; run with CGO_ENABLED=0 to compare saved logins here")
}

func TestConformanceAuthWithoutABrowser(t *testing.T) {
	type authCase struct {
		conformCase
		az        string
		storeOnly bool
	}
	cases := map[string]authCase{
		"login":                      {conformCase: conformCase{args: []string{"login"}}},
		"login to an invalid tenant": {conformCase: conformCase{args: []string{"login", "--tenant", "not a tenant!"}}},
		"mint-sas":                   {conformCase: conformCase{args: []string{"mint-sas", "--mode", "upload"}}},
		"check":                      {conformCase: conformCase{args: []string{"check", "--mode", "download"}}},
		"fetch":                      {conformCase: conformCase{args: []string{"fetch", "20261120-fancy-dassie-eadb"}}},
		"expired az login":           {conformCase: conformCase{args: []string{"mint-sas", "--mode", "download"}}, az: "expired"},
		"az not signed in":           {conformCase: conformCase{args: []string{"check", "--mode", "upload"}}, az: "signed-out"},
		"az prints no token":         {conformCase: conformCase{args: []string{"mint-sas", "--mode", "upload"}}, az: "garbage"},
		"az fails silently":          {conformCase: conformCase{args: []string{"mint-sas", "--mode", "upload"}}, az: "silent"},
		"az with an invalid tenant":  {conformCase: conformCase{args: []string{"mint-sas", "--mode", "upload", "--tenant", "bad/tenant"}}, az: "token"},
		"az and a tag without =":     {conformCase: conformCase{args: []string{"mint-sas", "--mode", "upload", "--tag", "storage"}}, az: "token"},
		"az and a tag without a key": {conformCase: conformCase{args: []string{"check", "--mode", "upload", "--tag", "=storage"}}, az: "token"},
		"logout with nothing saved":  {conformCase: conformCase{args: []string{"logout"}}, storeOnly: true},
		"logout while az signs in":   {conformCase: conformCase{args: []string{"logout"}}, az: "token", storeOnly: true},
		"logout with az signed out":  {conformCase: conformCase{args: []string{"logout"}}, az: "signed-out", storeOnly: true},
		"logout of a saved login":    {conformCase: conformCase{args: []string{"logout"}, setup: saveTestRecord}, storeOnly: true},
		"unusable saved login":       {conformCase: conformCase{args: []string{"mint-sas", "--mode", "upload"}, setup: saveTestRecord}, storeOnly: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if c.storeOnly {
				skipWhereStoresDiffer(t)
			}
			path := noAzPath(t)
			if c.az != "" {
				dir, _ := fakeAzDir(t, c.az)
				path = pathWith(dir)
			}
			c.env = map[string]string{"PATH": path}
			conform(t, c.conformCase)
		})
	}
}

func TestConformanceAuthLoginWithTheBrowser(t *testing.T) {
	powershell(t)
	f := newFakeAzure(t)
	w := newWorld(t, "ps", "", nil)
	r := f.runPS(t, w, nil, f.approveAll(t, imperialTenant), "login")
	want := "Signed in as " + fakeUser + ", but this build cannot remember logins; use 'az login' or --sas-env\n"
	if runtime.GOOS == "windows" {
		want = "Signed in as " + fakeUser + "; lbf will reuse this login until 'lbf logout'\n"
	}
	if r.code != 0 || r.stdout != want || r.stderr != "" {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", r.code, r.stdout, r.stderr)
	}
	record := filepath.Join(configDir(w), "lbf", "auth-record.json")
	raw, err := os.ReadFile(record)
	if runtime.GOOS != "windows" {
		if err == nil {
			t.Errorf("a build that cannot remember logins saved %s", raw)
		}
		return
	}
	must(t, err)
	wantRec, _ := json.Marshal(azidentity.AuthenticationRecord{Authority: "https://login.microsoftonline.com", ClientID: azureCLIClientID, HomeAccountID: fakeOID + "." + imperialTenant, TenantID: imperialTenant, Username: fakeUser, Version: "1.0"})
	if string(raw) != string(wantRec) {
		t.Errorf("record\n%s\nwant\n%s", raw, wantRec)
	}
	tokens, err := os.ReadFile(filepath.Join(w.home, "AppData", "Local", "lbf", "login-tokens.dat"))
	if err != nil || bytes.Contains(tokens, []byte("rt-")) {
		t.Errorf("saved tokens: %v, readable refresh token: %v", err, bytes.Contains(tokens, []byte("rt-")))
	}
}

func TestConformanceAuthSignInFailures(t *testing.T) {
	powershell(t)
	sep := strings.Repeat("-", 80)
	indent := func(s string) string {
		var b bytes.Buffer
		must(t, json.Indent(&b, []byte(s), "", "  "))
		return b.String()
	}
	tokenFailure := `{"error":"invalid_grant","error_description":"AADSTS50076: Due to a configuration change made by your administrator, or because you moved to a new location, you must use multi-factor authentication to access '797f4846-ba00-4fd7-ba43-dac1f8f63013'.","error_codes":[50076],"suberror":"basic_action","claims":"{\"access_token\":{\"capolids\":{\"essential\":true,\"values\":[\"x\"]}}}"}`
	for _, tc := range []struct {
		name   string
		args   []string
		env    map[string]string
		before func(f *fakeAzure)
		reply  func(state string) url.Values
		get    bool
		want   func(f *fakeAzure, state string) string
	}{
		{
			name:  "consent refused",
			args:  []string{"login"},
			reply: func(string) url.Values { return url.Values{"error": {"consent_required"}, "error_description": {"AADSTS65001: The user or administrator has not consented to use the application with ID '04b07795-8ddb-461a-bbee-02f9e1bf7b46' named 'Microsoft Azure CLI' <for> \"you\" & others."}} },
			want: func(*fakeAzure, string) string {
				return "error: browser sign-in failed: InteractiveBrowserCredential: AADSTS65001: The user or administrator has not consented to use the application with ID &#39;04b07795-8ddb-461a-bbee-02f9e1bf7b46&#39; named &#39;Microsoft Azure CLI&#39; &lt;for&gt; &#34;you&#34; &amp; others.\n"
			},
		},
		{
			name:  "state mismatch",
			args:  []string{"login"},
			reply: func(string) url.Values { return url.Values{"code": {"c"}, "state": {"forged"}} },
			want: func(_ *fakeAzure, state string) string {
				return "error: browser sign-in failed: InteractiveBrowserCredential: mismatched OAuth state, req(" + state + "), resp(forged)\n"
			},
		},
		{
			name: "redirect without a code",
			args: []string{"login"},
			reply: func(state string) url.Values { return url.Values{"state": {state}} },
			want: func(*fakeAzure, string) string {
				return "error: browser sign-in failed: InteractiveBrowserCredential: authorization code missing in response\n"
			},
		},
		{
			name: "redirect by GET",
			args: []string{"login"},
			get:  true,
			want: func(*fakeAzure, string) string {
				return "error: browser sign-in failed: InteractiveBrowserCredential: response was received via a GET operation, which is not supported\n"
			},
		},
		{
			name:   "conditional access at the token endpoint",
			args:   []string{"mint-sas", "--mode", "upload"},
			before: func(f *fakeAzure) { f.tokenFailure = &fakeFailure{status: 400, header: map[string]string{"Content-Type": "application/json"}, body: tokenFailure} },
			want: func(f *fakeAzure, _ string) string {
				return "[auth] not signed in; opening your browser to sign in\nerror: browser sign-in failed: InteractiveBrowserCredential authentication failed. \nPOST " + f.srv.URL + "/entra/" + imperialTenant + "/oauth2/v2.0/token\n" +
					sep + "\nRESPONSE 400: 400 Bad Request\n" + sep + "\n" + indent(tokenFailure) + "\n" + sep + "\n"
			},
		},
		{
			name: "unknown tenant",
			args: []string{"login", "--tenant", "nosuchtenant"},
			want: func(f *fakeAzure, _ string) string {
				body := `{"error":"invalid_tenant","error_description":"AADSTS90002: Tenant 'nosuchtenant' not found.","error_codes":[90002],"error_uri":"https://login.microsoftonline.com/error?code=90002"}`
				return "error: browser sign-in failed: InteractiveBrowserCredential authentication failed. \nGET " + f.srv.URL + "/entra/nosuchtenant/v2.0/.well-known/openid-configuration\n" +
					sep + "\nRESPONSE 400: 400 Bad Request\n" + sep + "\n" + indent(body) + "\n" + sep + "\n"
			},
		},
		{
			name: "nobody signs in",
			args: []string{"login"},
			env:  map[string]string{"LBF_TEST_LOGIN_TIMEOUT": "2"},
			want: func(*fakeAzure, string) string {
				return "error: browser sign-in timed out after 2s; if no browser opened, sign in with 'az login --tenant " + imperialTenant + " --use-device-code' instead\n"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeAzure(t)
			if tc.before != nil {
				tc.before(f)
			}
			w := newWorld(t, "ps", "", nil)
			state := ""
			browser := func(u *url.URL) {
				state = u.Query().Get("state")
				if tc.name == "nobody signs in" {
					return
				}
				if tc.get {
					resp, err := http.Get(u.Query().Get("redirect_uri") + "/?code=x&state=" + state)
					must(t, err)
					resp.Body.Close()
					if resp.StatusCode != http.StatusMethodNotAllowed {
						t.Errorf("GET redirect answered %d", resp.StatusCode)
					}
					return
				}
				tenant := imperialTenant
				status, body := f.approve(t, u, tenant, tc.reply)
				if tc.reply != nil && strings.Contains(tc.name, "consent") && (status != 200 || !strings.Contains(body, "Error details: error consent_required error_description: AADSTS65001")) {
					t.Errorf("failure page %d:\n%s", status, body)
				}
			}
			r := f.runPS(t, w, tc.env, browser, tc.args...)
			if want := tc.want(f, state); r.code != 1 || r.stdout != "" || r.stderr != want {
				t.Errorf("exit %d, stdout %q\nstderr:\n%s\nwant:\n%s", r.code, r.stdout, r.stderr, want)
			}
		})
	}
}

func readSASFile(t *testing.T, path, mode, container string) target {
	t.Helper()
	var tg target
	var err error
	captureStderr(t, func() { tg, err = readSASEnv(path, mode, container) })
	must(t, err)
	return tg
}

func TestConformanceAuthMintSAS(t *testing.T) {
	powershell(t)
	f := newFakeAzure(t)
	w := newWorld(t, "ps", "", nil)
	began := time.Now().UTC()
	r := f.runPS(t, w, nil, f.approveAll(t, imperialTenant), "mint-sas", "--mode", "upload", "--tag", "tag=storage-test", "--account", "lbfa")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	wantErr := "[auth] not signed in; opening your browser to sign in\n[auth] " + fakeUser + " via browser -> lbfa/bronze (subscription Sub A, requested=rwl)\n"
	if r.stderr != wantErr {
		t.Errorf("stderr:\n%s\nwant:\n%s", r.stderr, wantErr)
	}
	tg := readSASFile(t, filepath.Join(w.dir, "azure_sas.env"), "upload", "bronze")
	wantOut := fmt.Sprintf("Wrote azure_sas.env (lbfa/bronze, permissions=rwl, expires %s)\n", tg.Expiry.Format(time.RFC3339))
	if r.stdout != wantOut {
		t.Errorf("stdout %q, want %q", r.stdout, wantOut)
	}
	if tg.Account != "lbfa" || tg.Permissions != "rwl" || tg.User != fakeUser || tg.SubscriptionName != "Sub A" || tg.SubscriptionID != fakeSubA {
		t.Errorf("target %+v", tg)
	}
	if d := tg.Expiry.Sub(began.Add(sasLifetime)); d < -2*time.Second || d > time.Minute {
		t.Errorf("expiry %s is not %s from now", tg.Expiry, sasLifetime)
	}
	q, err := url.ParseQuery(tg.SAS)
	must(t, err)
	must(t, f.checkSAS(q, "lbfa", "bronze"))
	st, err := time.Parse(sas.TimeFormat, q.Get("st"))
	must(t, err)
	if want := f.goSAS(t, "lbfa", "bronze", "rwl", st, tg.Expiry); tg.SAS != want {
		t.Errorf("SAS differs from the Go SDK's:\nps %s\ngo %s", tg.SAS, want)
	}
	if !tg.Expiry.Equal(st.Add(15*time.Minute + sasLifetime)) {
		t.Errorf("window %s to %s", st, tg.Expiry)
	}
	f.mu.Lock()
	grants := strings.Join(f.grants, "\n")
	f.mu.Unlock()
	if want := "authorization_code " + signInScope + "\nrefresh_token https://storage.azure.com/.default openid offline_access profile"; grants != want {
		t.Errorf("token requests:\n%s\nwant:\n%s", grants, want)
	}
}

func TestConformanceAuthCheckSignedIn(t *testing.T) {
	powershell(t)
	f := newFakeAzure(t)
	for _, mode := range []string{"download", "upload"} {
		w := newWorld(t, "ps", "", nil)
		r := f.runPS(t, w, nil, f.approveAll(t, imperialTenant), "check", "--mode", mode, "--tag", "tag=storage-test", "--account", "lbfc")
		want := fmt.Sprintf("%s can %s lbfc/bronze, signed in via browser\n", fakeUser, mode)
		if r.code != 0 || r.stdout != want {
			t.Errorf("%s: exit %d stdout %q\n%s", mode, r.code, r.stdout, r.stderr)
		}
		if !strings.Contains(r.stderr, "[auth] "+fakeUser+" via browser -> lbfc/bronze (subscription Sub B, requested=") {
			t.Errorf("%s: stderr\n%s", mode, r.stderr)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if ops := strings.Join(f.blobOps, ","); ops != "GET bronze list,GET bronze list,PUT bronze/.lbf-check block" {
		t.Errorf("blob requests %s", ops)
	}
}

func goAccountMatches() []accountMatch {
	m := func(name, location, sub, id string) accountMatch {
		return accountMatch{Name: name, Location: location, Sub: &armsubscriptions.Subscription{DisplayName: new(sub), SubscriptionID: new(id)}}
	}
	return []accountMatch{m("lbfb", "uksouth", "Sub A", fakeSubA), m("lbfa", "ukwest", "Sub A", fakeSubA), m("lbfc", "uksouth", "Sub B", fakeSubB)}
}

func TestConformanceAuthAccountChoice(t *testing.T) {
	powershell(t)
	goErr := func(tag, want string) string {
		matches := goAccountMatches()
		if tag != "tag=storage-test" {
			matches = nil
		}
		_, err := chooseAccount(matches, tag, want, nil)
		return err.Error()
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"several tagged", []string{"--tag", "tag=storage-test"}, goErr("tag=storage-test", "")},
		{"unknown account", []string{"--tag", "tag=storage-test", "--account", "lbfz"}, goErr("tag=storage-test", "lbfz")},
		{"none tagged", []string{"--tag", "tag=nothing"}, goErr("tag=nothing", "")},
		{"tag key differs in case", []string{"--tag", "TAG=storage-test"}, goErr("TAG=storage-test", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeAzure(t)
			w := newWorld(t, "ps", "", nil)
			r := f.runPS(t, w, nil, f.approveAll(t, imperialTenant), append([]string{"mint-sas", "--mode", "download"}, tc.args...)...)
			want := "[auth] not signed in; opening your browser to sign in\nerror: " + tc.want + "\n"
			if r.code != 1 || r.stderr != want {
				t.Errorf("exit %d\nstderr:\n%s\nwant:\n%s", r.code, r.stderr, want)
			}
		})
	}
}

func goResponseError(method, rawURL string, status int, header map[string]string, body string) string {
	req, _ := http.NewRequest(method, rawURL, nil)
	h := http.Header{}
	for k, v := range header {
		h.Set(k, v)
	}
	resp := &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: req}
	return azruntime.NewResponseError(resp).Error()
}

func TestConformanceAuthAzureErrors(t *testing.T) {
	powershell(t)
	armDenied := `{"error":{"code":"AuthorizationFailed","message":"The client 'researcher@ic.ac.uk' with object id '6b3c' does not have authorization to perform action 'Microsoft.Resources/subscriptions/read'."}}`
	keyDenied := "\ufeff<?xml version=\"1.0\" encoding=\"utf-8\"?><Error><Code>AuthorizationPermissionMismatch</Code><Message>This request is not authorized to perform this operation using this permission.\nRequestId:1\nTime:2026-11-20T12:00:00.0000000Z</Message></Error>"
	for _, tc := range []struct {
		name string
		set  func(f *fakeAzure)
		want func(f *fakeAzure) string
	}{
		{
			name: "subscriptions refused",
			set: func(f *fakeAzure) {
				f.armFailures["/subscriptions"] = fakeFailure{403, map[string]string{"Content-Type": "application/json"}, armDenied}
			},
			want: func(f *fakeAzure) string {
				return "listing subscriptions failed (an access or connectivity problem, not a tagging problem): " + goResponseError("GET", f.srv.URL+"/arm/subscriptions?api-version=2022-12-01", 403, nil, armDenied)
			},
		},
		{
			name: "storage accounts not found",
			set: func(f *fakeAzure) {
				f.armFailures["/subscriptions/"+fakeSubB+"/providers/Microsoft.Storage/storageAccounts"] = fakeFailure{404, map[string]string{"x-ms-error-code": "SubscriptionNotFound"}, ""}
			},
			want: func(f *fakeAzure) string {
				return "listing storage accounts in Sub B failed: " + goResponseError("GET", f.srv.URL+"/arm/subscriptions/"+fakeSubB+"/providers/Microsoft.Storage/storageAccounts?api-version=2024-01-01", 404, map[string]string{"x-ms-error-code": "SubscriptionNotFound"}, "")
			},
		},
		{
			name: "delegation key refused",
			set: func(f *fakeAzure) {
				f.keyFailures["lbfa"] = fakeFailure{403, map[string]string{"x-ms-error-code": "AuthorizationPermissionMismatch", "Content-Type": "application/xml"}, keyDenied}
			},
			want: func(f *fakeAzure) string {
				return "getting a user delegation key for lbfa needs the 'Storage Blob Delegator' role at storage account scope: " + goResponseError("POST", f.srv.URL+"/blob/lbfa/?restype=service&comp=userdelegationkey", 403, map[string]string{"x-ms-error-code": "AuthorizationPermissionMismatch"}, keyDenied)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeAzure(t)
			tc.set(f)
			w := newWorld(t, "ps", "", nil)
			r := f.runPS(t, w, nil, f.approveAll(t, imperialTenant), "mint-sas", "--mode", "download", "--tag", "tag=storage-test", "--account", "lbfa")
			want := "[auth] not signed in; opening your browser to sign in\nerror: " + tc.want(f) + "\n"
			if r.code != 1 || r.stderr != want {
				t.Errorf("exit %d\nstderr:\n%s\nwant:\n%s", r.code, r.stderr, want)
			}
		})
	}
}

func TestConformanceAuthAzLogin(t *testing.T) {
	powershell(t)
	f := newFakeAzure(t)
	w := newWorld(t, "ps", "", nil)
	dir, calls := fakeAzDir(t, "token")
	r := f.runPS(t, w, map[string]string{"PATH": pathWith(dir)}, nil, "mint-sas", "--mode", "download", "--tag", "tag=storage-test", "--account", "lbfb", "--container", "silver", "--out", "silver.env")
	if r.code != 0 || r.stderr != "[auth] az@ic.ac.uk via az login -> lbfb/silver (subscription Sub A, requested=rl)\n" {
		t.Fatalf("exit %d\nstderr:\n%s", r.code, r.stderr)
	}
	tg := readSASFile(t, filepath.Join(w.dir, "silver.env"), "download", "silver")
	q, err := url.ParseQuery(tg.SAS)
	must(t, err)
	must(t, f.checkSAS(q, "lbfb", "silver"))
	raw, err := os.ReadFile(calls)
	must(t, err)
	got := strings.ReplaceAll(strings.TrimSpace(string(raw)), "\r\n", "\n")
	want := "account get-access-token -o json --resource https://management.azure.com --tenant " + imperialTenant + "\naccount get-access-token -o json --resource https://storage.azure.com --tenant " + imperialTenant
	if got != want {
		t.Errorf("az ran with:\n%s\nwant:\n%s", got, want)
	}
}

func TestConformanceAuthSavedLogin(t *testing.T) {
	powershell(t)
	f := newFakeAzure(t)
	w := newWorld(t, "ps", "", nil)
	if runtime.GOOS != "windows" {
		saveTestRecord(t, w)
		r := f.runPS(t, w, nil, nil, "logout")
		if r.code != 0 || r.stdout != "Signed saved@ic.ac.uk out of lbf\n" {
			t.Errorf("logout of a record without tokens: exit %d %q %q", r.code, r.stdout, r.stderr)
		}
		saveTestRecord(t, w)
		r = f.runPS(t, w, nil, f.approveAll(t, imperialTenant), "check", "--mode", "download", "--tag", "tag=storage-test", "--account", "lbfa")
		want := "[auth] cannot use the login 'lbf login' saved for saved@ic.ac.uk: this build of lbf cannot remember logins\n[auth] not signed in; opening your browser to sign in\n"
		if r.code != 0 || !strings.HasPrefix(r.stderr, want) {
			t.Errorf("exit %d\nstderr:\n%s\nwant it to start:\n%s", r.code, r.stderr, want)
		}
		return
	}
	r := f.runPS(t, w, nil, f.approveAll(t, imperialTenant), "login")
	if r.code != 0 {
		t.Fatalf("login: %s", r.stderr)
	}
	r = f.runPS(t, w, nil, nil, "mint-sas", "--mode", "upload", "--tag", "tag=storage-test", "--account", "lbfa")
	if r.code != 0 || r.stderr != "[auth] "+fakeUser+" via lbf login -> lbfa/bronze (subscription Sub A, requested=rwl)\n" {
		t.Errorf("reusing the saved login: exit %d\n%s", r.code, r.stderr)
	}
	f.mu.Lock()
	clear(f.refresh)
	f.mu.Unlock()
	r = f.runPS(t, w, nil, f.approveAll(t, imperialTenant), "check", "--mode", "download", "--tag", "tag=storage-test", "--account", "lbfa")
	want := "[auth] cannot use the login 'lbf login' saved for " + fakeUser + ": InteractiveBrowserCredential can't acquire a token without user interaction. Call Authenticate to authenticate a user interactively\n[auth] not signed in; opening your browser to sign in\n"
	if r.code != 0 || !strings.HasPrefix(r.stderr, want) {
		t.Errorf("revoked refresh token: exit %d\nstderr:\n%s", r.code, r.stderr)
	}
	r = f.runPS(t, w, nil, nil, "logout")
	if r.code != 0 || r.stdout != "Signed "+fakeUser+" out of lbf and deleted its saved tokens\n" {
		t.Errorf("logout: exit %d %q %q", r.code, r.stdout, r.stderr)
	}
	for _, p := range []string{filepath.Join(configDir(w), "lbf", "auth-record.json"), filepath.Join(w.home, "AppData", "Local", "lbf", "login-tokens.dat")} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived logout: %v", p, err)
		}
	}
	r = f.runPS(t, w, nil, nil, "logout")
	if r.stdout != "lbf had no saved login\n" {
		t.Errorf("second logout: %q %q", r.stdout, r.stderr)
	}
}

// Runs body in PowerShell with the built lbf.ps1 dot-sourced and input as $data, returning stdout.
func psEval(t *testing.T, body string, input any) (string, string) {
	t.Helper()
	ps := powershell(t)
	script, err := builtScript()
	must(t, err)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.json")
	raw, err := json.Marshal(input)
	must(t, err)
	must(t, os.WriteFile(in, raw, 0o644))
	command := fmt.Sprintf("[Console]::OutputEncoding = New-Object Text.UTF8Encoding $false; $env:LBF_TEST_NO_MAIN = '1'; . '%s'; $data = [IO.File]::ReadAllText('%s') | ConvertFrom-Json\n%s",
		strings.ReplaceAll(script, "'", "''"), strings.ReplaceAll(in, "'", "''"), body)
	cmd := psCommand(ps, command)
	cmd.Env = append(os.Environ(), "LBF_NO_UPDATE_CHECK=1", "TERM=dumb")
	r := runCmd(t, cmd)
	if r.code != 0 {
		t.Fatalf("PowerShell exited %d:\n%s", r.code, r.stderr)
	}
	return r.stdout, r.stderr
}

type pickResult struct {
	Index int    `json:"index"`
	Error string `json:"error"`
	Out   string `json:"out"`
}

func goPick(pick func(context.Context, io.Reader, io.Writer, string, []string) (int, error), chunks []string, labels []string) pickResult {
	readers := make([]io.Reader, len(chunks))
	for i, c := range chunks {
		readers[i] = strings.NewReader(c)
	}
	var out strings.Builder
	i, err := pick(context.Background(), io.MultiReader(readers...), &out, "Choose:", labels)
	r := pickResult{Index: i, Out: out.String()}
	if err != nil {
		r.Index, r.Error = -1, err.Error()
	}
	return r
}

func scanLines(s string) []string {
	lines := []string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func TestConformanceAuthPickers(t *testing.T) {
	type pickCase struct {
		Chunks []string `json:"chunks"`
		Labels []string `json:"labels"`
	}
	abc := []string{"a", "b", "c"}
	wide := []string{strings.Repeat("a", 200), strings.Repeat("b", 200)}
	ten := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	var arrows []pickCase
	for _, keys := range []string{"\r", "\x1b[B\r", "\x1b[B\x1b[B\x1b[B\x1b[B\n", "\x1b[B\x1b[A\x1b[A\r", "jjk\r", "\x1bOB\r", "\x1b[C\r",
		"\x03", "q", "\x1b", "", "\x1b[B", "3", "2\r", "x\r2", "x\r\x1b[B\r", "\x1b[1;2B\r", "x\r", "9\r", "0\r", " \r", "lbfb\r", "\x1b[15~\r", "\x1bB\r", "\u00e9\r"} {
		arrows = append(arrows, pickCase{[]string{keys}, abc})
	}
	arrows = append(arrows,
		pickCase{[]string{"\x1b[", "B", "\r"}, abc},
		pickCase{[]string{"\x1b[B", "\r"}, wide},
		pickCase{[]string{"x", "1", "\x1b[B", "\r"}, ten},
		pickCase{[]string{"\x1b[B", "\x1b[B", "\r"}, []string{"S\u00fcb \u00c4  \u65e5\u672c", strings.Repeat("\u00e9", 60)}},
	)
	var numbered []pickCase
	for _, in := range []string{"x\n9\n\n 2 \r\n", "9\n", "", "+1\n", "02", " -1\n0\n3\n2\n", "1 2\n"} {
		numbered = append(numbered, pickCase{scanLines(in), []string{"a", "b"}})
	}
	var wantArrows, wantNumbered []pickResult
	for _, c := range arrows {
		wantArrows = append(wantArrows, goPick(pickArrows, c.Chunks, c.Labels))
	}
	for i, in := range []string{"x\n9\n\n 2 \r\n", "9\n", "", "+1\n", "02", " -1\n0\n3\n2\n", "1 2\n"} {
		wantNumbered = append(wantNumbered, goPick(pickNumbered, []string{in}, numbered[i].Labels))
	}
	stdout, _ := psEval(t, `
function Invoke-PickCase($Kind, $Case) {
    $script:pending = New-Object System.Collections.Queue
    foreach ($k in @($Case.chunks)) { if ($Kind -ne 'arrows' -or -not [string]::IsNullOrEmpty([string]$k)) { $script:pending.Enqueue([string]$k) } }
    $sb = New-Object System.Text.StringBuilder
    $r = @{ index = -1; error = ''; out = '' }
    $next = { if ($script:pending.Count -gt 0) { $script:pending.Dequeue() } else { $null } }
    try {
        if ($Kind -eq 'arrows') { $r.index = [int](Invoke-LbfPickArrows -Read $next -Out $sb -Prompt 'Choose:' -Labels ([string[]]@($Case.labels))) }
        else { $r.index = [int](Invoke-LbfPickNumbered -ReadLine $next -Out $sb -Prompt 'Choose:' -Labels ([string[]]@($Case.labels))) }
    } catch { $r.error = Get-LbfErrorText $_ }
    $r.out = $sb.ToString()
    ConvertTo-Json $r -Compress
}
foreach ($c in @($data.arrows)) { Invoke-PickCase 'arrows' $c }
foreach ($c in @($data.numbered)) { Invoke-PickCase 'numbered' $c }
`, map[string]any{"arrows": arrows, "numbered": numbered})
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(stdout, "\r\n", "\n")), "\n")
	want := append(wantArrows, wantNumbered...)
	if len(lines) != len(want) {
		t.Fatalf("PowerShell gave %d results for %d cases:\n%s", len(lines), len(want), stdout)
	}
	for i, l := range lines {
		var got pickResult
		must(t, json.Unmarshal([]byte(l), &got))
		kind, c := "arrows", pickCase{}
		if i < len(arrows) {
			c = arrows[i]
		} else {
			kind, c = "numbered", numbered[i-len(arrows)]
		}
		if got != want[i] {
			t.Errorf("%s %q %q:\nps %#v\ngo %#v", kind, c.Chunks, c.Labels, got, want[i])
		}
	}
}

func TestConformanceAuthChooseAccount(t *testing.T) {
	type choiceCase struct {
		Tag     string `json:"tag"`
		Want    string `json:"want"`
		Picker  string `json:"picker"`
		Matches int    `json:"matches"`
	}
	all := []accountMatch{}
	m := func(name, location, sub string) accountMatch {
		return accountMatch{Name: name, Location: location, Sub: &armsubscriptions.Subscription{DisplayName: new(sub), SubscriptionID: new("id-" + name)}}
	}
	all = append(all, m("lbfb", "uksouth", "Sub B"), m("lbfa", "uksouth", "S\u00fcb \u00c4"), m("lbfzz", "ukwest", "Sub with a much longer name"))
	cases := []choiceCase{
		{"tag=x", "", "never", 1}, {"tag=x", "lbfb", "never", 2}, {"tag=x", "other", "never", 2}, {"tag=x", "", "never", 0},
		{"tag=x", "", "none", 2}, {"tag=x", "", "none", 3}, {"tag=x", "", "second", 3}, {"tag=x", "", "cancel", 2}, {"tag \"q\"", "Lbfa", "none", 3},
	}
	type choiceResult struct {
		Chosen string   `json:"chosen"`
		Error  string   `json:"error"`
		Labels []string `json:"labels"`
		Prompt string   `json:"prompt"`
	}
	var want []choiceResult
	var wantLog strings.Builder
	for _, c := range cases {
		matches := append([]accountMatch(nil), all[:c.Matches]...)
		var r choiceResult
		var pick picker
		switch c.Picker {
		case "never":
			pick = func(string, []string) (int, error) { r.Error = "picker called"; return 0, nil }
		case "second":
			pick = func(p string, l []string) (int, error) { r.Prompt, r.Labels = p, l; return 1, nil }
		case "cancel":
			pick = func(p string, l []string) (int, error) { r.Prompt, r.Labels = p, l; return 0, errNoChoice }
		}
		var got accountMatch
		var err error
		wantLog.WriteString(captureStderr(t, func() { got, err = chooseAccount(matches, c.Tag, c.Want, pick) }))
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Chosen = got.Name
		}
		want = append(want, r)
	}
	var psMatches []map[string]string
	for _, a := range all {
		psMatches = append(psMatches, map[string]string{"Name": a.Name, "Location": a.Location, "SubscriptionName": *a.Sub.DisplayName, "SubscriptionID": *a.Sub.SubscriptionID})
	}
	stdout, stderr := psEval(t, `
foreach ($c in @($data.cases)) {
    $list = @()
    $i = 0
    foreach ($m in @($data.matches)) { if ($i -lt $c.matches) { $list += @{ Name = $m.Name; Location = $m.Location; SubscriptionName = $m.SubscriptionName; SubscriptionID = $m.SubscriptionID } }; $i++ }
    $script:r = @{ chosen = ''; error = ''; labels = $null; prompt = '' }
    $pick = $null
    switch ($c.picker) {
        'never' { $pick = { param($p, $l) $script:r.error = 'picker called'; 0 } }
        'second' { $pick = { param($p, $l) $script:r.prompt = $p; $script:r.labels = [string[]]$l; 1 } }
        'cancel' { $pick = { param($p, $l) $script:r.prompt = $p; $script:r.labels = [string[]]$l; throw (New-LbfError 'no storage account chosen') } }
    }
    try { $script:r.chosen = (Select-LbfAccount -Matches $list -Tag $c.tag -Want $c.want -Picker $pick).Name }
    catch { $script:r.error = Get-LbfErrorText $_ }
    ConvertTo-Json $script:r -Compress
}
`, map[string]any{"cases": cases, "matches": psMatches})
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(stdout, "\r\n", "\n")), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("PowerShell gave %d results for %d cases:\n%s", len(lines), len(cases), stdout)
	}
	for i, l := range lines {
		var got choiceResult
		must(t, json.Unmarshal([]byte(l), &got))
		if fmt.Sprint(got) != fmt.Sprint(want[i]) {
			t.Errorf("%+v:\nps %q\ngo %q", cases[i], got, want[i])
		}
	}
	if got := strings.ReplaceAll(stderr, "\r\n", "\n"); got != wantLog.String() {
		t.Errorf("logged %q, want %q", got, wantLog.String())
	}
}

func TestConformanceAuthHelpers(t *testing.T) {
	type reasonCase struct {
		Stdin, Stderr bool
		Goos          string
		Env           map[string]string
	}
	var reasons []reasonCase
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, env := range []map[string]string{{}, {"DISPLAY": ":0"}, {"WAYLAND_DISPLAY": "wayland-0"}} {
			for _, in := range []bool{true, false} {
				for _, errTerm := range []bool{true, false} {
					reasons = append(reasons, reasonCase{in, errTerm, goos, env})
				}
			}
		}
	}
	var wantReasons []string
	for _, c := range reasons {
		wantReasons = append(wantReasons, browserUnavailable(c.Stdin, c.Stderr, c.Goos, func(k string) string { return c.Env[k] }))
	}

	type httpCase struct {
		Method string            `json:"method"`
		URL    string            `json:"url"`
		Status int               `json:"status"`
		Header map[string]string `json:"header"`
		Body   string            `json:"body"`
	}
	bodies := []string{
		`{"error":{"code":"AuthorizationFailed","message":"no <access> & \u2028 \"here\""}}`,
		"{\"odata.error\":{\"code\":\"Odata\",\"message\":{\"lang\":\"en\",\"value\":\"x\"}}}\n",
		`{"code":"TopLevel","details":[],"empty":{},"n":[1,2.5e3,true,null]}`,
		`{"error":"not an object","code":"Ignored"}`,
		`["error"]`,
		"\ufeff<?xml version=\"1.0\" encoding=\"utf-8\"?><Error><Code>ContainerNotFound</Code><Message>The specified container does not exist.</Message></Error>",
		"<m:error><m:code> Spaced_Code </m:code></m:error>",
		"not json at all",
		"",
		"  {\"a\" : [ ] , \"b\":{ \"c\" :\"d\\\"e\" } }  \n",
	}
	var https []httpCase
	for i, b := range bodies {
		h := map[string]string{}
		if i == 7 {
			h["x-ms-error-code"] = "HeaderCode"
		}
		https = append(https, httpCase{"GET", "https://management.azure.com/subscriptions/a%20b/x?api-version=1", 403, h, b})
	}
	https = append(https, httpCase{"POST", "http://127.0.0.1:8080/acct/?restype=service&comp=userdelegationkey", 500, nil, ""}, httpCase{"GET", "https://example.org", 429, nil, "{}"})
	var wantHTTP []string
	for _, c := range https {
		wantHTTP = append(wantHTTP, goResponseError(c.Method, c.URL, c.Status, c.Header, c.Body))
	}
	var wantIndent []string
	for _, b := range bodies {
		var buf bytes.Buffer
		if err := json.Indent(&buf, []byte(b), "", "  "); err != nil {
			buf.Reset()
			buf.WriteString(b)
		}
		wantIndent = append(wantIndent, buf.String())
	}

	stdout, _ := psEval(t, `
foreach ($c in @($data.reasons)) {
    $envs = @{}
    foreach ($p in $c.Env.PSObject.Properties) { $envs[$p.Name] = $p.Value }
    ConvertTo-Json @{ v = (Get-LbfNoBrowserReason -StdinTerminal $c.Stdin -StderrTerminal $c.Stderr -Goos $c.Goos -Environment $envs) } -Compress
}
foreach ($c in @($data.http)) {
    $h = @{}
    if ($null -ne $c.header) { foreach ($p in $c.header.PSObject.Properties) { $h[$p.Name.ToLowerInvariant()] = $p.Value } }
    $bytes = (New-Object Text.UTF8Encoding $false).GetBytes([string]$c.body)
    $resp = @{ Method = $c.method; Uri = $c.url; Status = [int]$c.status; Reason = ''; Headers = $h; Body = $bytes; Text = [string]$c.body }
    ConvertTo-Json @{ v = (Format-LbfAzureResponseError $resp) } -Compress
}
foreach ($b in @($data.bodies)) { ConvertTo-Json @{ v = (Format-LbfGoIndent ([string]$b)) } -Compress }
`, map[string]any{"reasons": reasons, "http": https, "bodies": bodies})
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(stdout, "\r\n", "\n")), "\n")
	want := append(append(append([]string{}, wantReasons...), wantHTTP...), wantIndent...)
	if len(lines) != len(want) {
		t.Fatalf("PowerShell gave %d results for %d cases:\n%s", len(lines), len(want), stdout)
	}
	for i, l := range lines {
		var got struct{ V string }
		must(t, json.Unmarshal([]byte(l), &got))
		if got.V != want[i] {
			t.Errorf("case %d:\nps %q\ngo %q", i, got.V, want[i])
		}
	}
}
