package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const (
	imperialTenant = "2b897507-ee8c-4575-830b-4f8267c3d307"
	// The Azure CLI's public client ID: Imperial's sign-in policies already allow it.
	azureCLIClientID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"
)

var armScope = policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}}

// Tries, in order: a login saved by `lbf login`, an existing `az login`, then the browser.
func credential(ctx context.Context, tenant string) (azcore.TokenCredential, string, error) {
	if rec, ok := loadRecord(); ok {
		if probe, err := browserCredential(tenant, rec, true); err == nil {
			if _, err := probe.GetToken(ctx, armScope); err == nil {
				// Later scopes may still need the browser if the cached refresh token cannot serve them.
				cred, err := browserCredential(tenant, rec, false)
				return cred, "lbf login", err
			}
		}
	}

	if cli, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{TenantID: tenant}); err == nil {
		if _, err := cli.GetToken(ctx, armScope); err == nil {
			return cli, "az login", nil
		}
	}

	logf("[auth] not signed in; opening your browser to sign in\n")
	cred, err := login(ctx, tenant)
	return cred, "browser", err
}

func login(ctx context.Context, tenant string) (azcore.TokenCredential, error) {
	cred, err := browserCredential(tenant, azidentity.AuthenticationRecord{}, false)
	if err != nil {
		return nil, err
	}
	rec, err := cred.Authenticate(ctx, &armScope)
	if err != nil {
		return nil, fmt.Errorf("browser sign-in failed: %w", err)
	}
	if hasPersistentCache() {
		if err := saveRecord(rec); err != nil {
			return nil, err
		}
	}
	return cred, nil
}

func logout() (bool, error) {
	path, err := recordPath()
	if err != nil {
		return false, err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func browserCredential(tenant string, rec azidentity.AuthenticationRecord, silentOnly bool) (*azidentity.InteractiveBrowserCredential, error) {
	c, _ := tokenCache()
	return azidentity.NewInteractiveBrowserCredential(&azidentity.InteractiveBrowserCredentialOptions{
		ClientID:                       azureCLIClientID,
		TenantID:                       tenant,
		Cache:                          c,
		AuthenticationRecord:           rec,
		DisableAutomaticAuthentication: silentOnly,
	})
}

func hasPersistentCache() bool {
	_, ok := tokenCache()
	return ok
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
