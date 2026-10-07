//go:build windows || (darwin && cgo)

package main

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity/cache"
)

type fakeSTS struct{ tokenRequests atomic.Int32 }

func (f *fakeSTS) Do(req *http.Request) (*http.Response, error) {
	body := `{"token_endpoint": "https://{host}/{tenant}/oauth2/v2.0/token", "issuer": "https://{host}/{tenant}/v2.0", "authorization_endpoint": "https://{host}/{tenant}/oauth2/v2.0/authorize"}`
	if strings.HasSuffix(req.URL.Path, "/token") {
		f.tokenRequests.Add(1)
		body = `{"access_token": "at", "expires_in": 3600, "token_type": "Bearer"}`
	}
	body = strings.NewReplacer("{host}", req.URL.Host, "{tenant}", strings.Split(req.URL.Path, "/")[1]).Replace(body)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func tokenRequestsWith(t *testing.T, name string) int32 {
	t.Helper()
	c, err := cache.New(&cache.Options{Name: name})
	if err != nil {
		t.Skipf("no OS credential store here: %v", err)
	}
	sts := &fakeSTS{}
	cred, err := azidentity.NewClientSecretCredential("11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "secret", &azidentity.ClientSecretCredentialOptions{
		ClientOptions:            azcore.ClientOptions{Transport: sts},
		Cache:                    c,
		DisableInstanceDiscovery: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cred.GetToken(context.Background(), armScope); err != nil {
		t.Fatal(err)
	}
	return sts.tokenRequests.Load()
}

func TestClearTokenCacheDeletesWhatAzidentityCached(t *testing.T) {
	name := fmt.Sprintf("lbf-test-%d", rand.Int())
	t.Cleanup(func() {
		clearTokenCache(name)
		dir, _ := os.UserHomeDir()
		if runtime.GOOS == "windows" {
			dir = os.Getenv("LOCALAPPDATA")
		}
		leftovers, _ := filepath.Glob(filepath.Join(dir, ".IdentityService", name+"*"))
		for _, p := range leftovers {
			os.Remove(p)
		}
	})

	if n := tokenRequestsWith(t, name); n != 1 {
		t.Fatalf("first sign-in made %d token requests, want 1", n)
	}
	if n := tokenRequestsWith(t, name); n != 0 {
		t.Fatalf("a second process made %d token requests, so the token was never cached", n)
	}
	found, err := clearTokenCache(name)
	if err != nil || !found {
		t.Fatalf("clearTokenCache = %v, %v; want true, nil", found, err)
	}
	if n := tokenRequestsWith(t, name); n != 1 {
		t.Fatalf("after clearing, %d token requests, want 1: the cached token survived", n)
	}
	clearTokenCache(name)
	if found, err := clearTokenCache(name); err != nil || found {
		t.Fatalf("clearing an empty cache = %v, %v; want false, nil", found, err)
	}
}
