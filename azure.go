package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
)

type target struct {
	Account          string
	Container        string
	SAS              string
	Expiry           time.Time
	Permissions      string
	User             string
	SubscriptionName string
	SubscriptionID   string
}

func (t target) containerURL() string {
	return fmt.Sprintf("https://%s.blob.core.windows.net/%s", t.Account, t.Container)
}

func (t target) client() (*container.Client, error) {
	return container.NewClientWithNoCredential(t.containerURL()+"?"+t.SAS, nil)
}

func mintTarget(ctx context.Context, tenant, tag, containerName, mode string) (target, error) {
	cred, source, err := credential(ctx, tenant)
	if err != nil {
		return target{}, err
	}

	user, err := signedInUser(ctx, cred)
	if err != nil {
		return target{}, err
	}

	account, sub, err := findAccount(ctx, cred, tag)
	if err != nil {
		return target{}, err
	}

	svc, err := service.NewClient(fmt.Sprintf("https://%s.blob.core.windows.net/", account), cred, nil)
	if err != nil {
		return target{}, err
	}

	start := time.Now().UTC()
	expiry := start.Add(sasLifetime)
	udc, err := svc.GetUserDelegationCredential(ctx, service.KeyInfo{
		Start:  to.Ptr(start.Format(sas.TimeFormat)),
		Expiry: to.Ptr(expiry.Format(sas.TimeFormat)),
	}, nil)
	if err != nil {
		return target{}, fmt.Errorf("getting a user delegation key for %s needs the 'Storage Blob Delegator' role at storage account scope: %w", account, err)
	}

	perms := (&sas.ContainerPermissions{Read: true, List: true, Write: mode == "upload"}).String()
	qp, err := sas.BlobSignatureValues{
		Protocol:      sas.ProtocolHTTPS,
		StartTime:     start,
		ExpiryTime:    expiry,
		Permissions:   perms,
		ContainerName: containerName,
	}.SignWithUserDelegation(udc)
	if err != nil {
		return target{}, err
	}

	subName := deref(sub.DisplayName)
	logf("[auth] %s via %s -> %s/%s (subscription %s, requested=%s)\n",
		user, source, account, containerName, subName, perms)

	return target{
		Account:          account,
		Container:        containerName,
		SAS:              qp.Encode(),
		Expiry:           expiry,
		Permissions:      perms,
		User:             user,
		SubscriptionName: subName,
		SubscriptionID:   deref(sub.SubscriptionID),
	}, nil
}

// Searches every subscription the user can see; exactly one account may carry the tag.
func findAccount(ctx context.Context, cred azcore.TokenCredential, tag string) (string, *armsubscriptions.Subscription, error) {
	key, value, ok := strings.Cut(tag, "=")
	if !ok || key == "" {
		return "", nil, fmt.Errorf("--tag must be KEY=VALUE, got %q", tag)
	}

	subs, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return "", nil, err
	}

	type match struct {
		account string
		sub     *armsubscriptions.Subscription
	}
	var matches []match

	subPager := subs.NewListPager(nil)
	for subPager.More() {
		page, err := subPager.NextPage(ctx)
		if err != nil {
			return "", nil, fmt.Errorf("listing subscriptions failed (an access or connectivity problem, not a tagging problem): %w", err)
		}
		for _, sub := range page.Value {
			if deref(sub.State) != armsubscriptions.SubscriptionStateEnabled {
				continue
			}
			accounts, err := armstorage.NewAccountsClient(*sub.SubscriptionID, cred, nil)
			if err != nil {
				return "", nil, err
			}
			accPager := accounts.NewListPager(nil)
			for accPager.More() {
				accPage, err := accPager.NextPage(ctx)
				if err != nil {
					return "", nil, fmt.Errorf("listing storage accounts in %s failed: %w", deref(sub.DisplayName), err)
				}
				for _, acc := range accPage.Value {
					if v := acc.Tags[key]; v != nil && *v == value {
						matches = append(matches, match{*acc.Name, sub})
					}
				}
			}
		}
	}

	switch len(matches) {
	case 0:
		return "", nil, fmt.Errorf("no storage account tagged %q is visible to you; ask your Azure administrator to apply it", tag)
	case 1:
		return matches[0].account, matches[0].sub, nil
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.account
	}
	return "", nil, fmt.Errorf("%d storage accounts are tagged %q (%s); exactly one must be", len(matches), tag, strings.Join(names, ", "))
}

func signedInUser(ctx context.Context, cred azcore.TokenCredential) (string, error) {
	tok, err := cred.GetToken(ctx, armScope)
	if err != nil {
		return "", err
	}
	var claims map[string]any
	if parts := strings.Split(tok.Token, "."); len(parts) == 3 {
		if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			_ = json.Unmarshal(payload, &claims)
		}
	}
	for _, k := range []string{"upn", "email", "preferred_username", "unique_name"} {
		if s, ok := claims[k].(string); ok && s != "" {
			return s, nil
		}
	}
	return "unknown", nil
}
