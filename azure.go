package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
	endpoint         string // an emulator's account URL, in tests
}

func (t target) containerURL() string {
	if t.endpoint != "" {
		return t.endpoint + "/" + t.Container
	}
	return fmt.Sprintf("https://%s.blob.core.windows.net/%s", t.Account, t.Container)
}

func (t target) client() (*container.Client, error) {
	return container.NewClientWithNoCredential(t.containerURL()+"?"+t.SAS, nil)
}

func mintTarget(ctx context.Context, tenant, tag, accountName, containerName, mode string) (target, error) {
	defer status("")
	status("Signing in")
	cred, source, err := credential(ctx, tenant)
	if err != nil {
		return target{}, err
	}

	user, err := signedInUser(ctx, cred)
	if err != nil {
		return target{}, err
	}

	status("Finding the storage account tagged " + tag)
	matches, err := findAccounts(ctx, cred, tag)
	status("")
	if err != nil {
		return target{}, err
	}
	chosen, err := chooseAccount(matches, tag, accountName, terminalPicker())
	if err != nil {
		return target{}, err
	}
	account, sub := chosen.Name, chosen.Sub

	svc, err := service.NewClient(fmt.Sprintf("https://%s.blob.core.windows.net/", account), cred, nil)
	if err != nil {
		return target{}, err
	}

	status("Getting a " + mode + " key for " + account)
	start := time.Now().UTC()
	expiry := start.Add(sasLifetime)
	udc, err := svc.GetUserDelegationCredential(ctx, service.KeyInfo{
		Start:  new(start.Format(sas.TimeFormat)),
		Expiry: new(expiry.Format(sas.TimeFormat)),
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

type accountMatch struct {
	Name, Location string
	Sub            *armsubscriptions.Subscription
}

// Searches every subscription the user can see.
func findAccounts(ctx context.Context, cred azcore.TokenCredential, tag string) ([]accountMatch, error) {
	key, value, ok := strings.Cut(tag, "=")
	if !ok || key == "" {
		return nil, fmt.Errorf("--tag must be KEY=VALUE, got %q", tag)
	}

	subs, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return nil, err
	}

	var matches []accountMatch
	subPager := subs.NewListPager(nil)
	for subPager.More() {
		page, err := subPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing subscriptions failed (an access or connectivity problem, not a tagging problem): %w", err)
		}
		for _, sub := range page.Value {
			if deref(sub.State) != armsubscriptions.SubscriptionStateEnabled {
				continue
			}
			accounts, err := armstorage.NewAccountsClient(*sub.SubscriptionID, cred, nil)
			if err != nil {
				return nil, err
			}
			accPager := accounts.NewListPager(nil)
			for accPager.More() {
				accPage, err := accPager.NextPage(ctx)
				if err != nil {
					return nil, fmt.Errorf("listing storage accounts in %s failed: %w", deref(sub.DisplayName), err)
				}
				for _, acc := range accPage.Value {
					if v := acc.Tags[key]; v != nil && *v == value {
						matches = append(matches, accountMatch{*acc.Name, deref(acc.Location), sub})
					}
				}
			}
		}
	}
	return matches, nil
}

// pick is nil when there is no terminal to ask at.
func chooseAccount(matches []accountMatch, tag, want string, pick picker) (accountMatch, error) {
	if len(matches) == 0 {
		return accountMatch{}, fmt.Errorf("no storage account tagged %q is visible to you; ask your Azure administrator to apply it", tag)
	}
	slices.SortFunc(matches, func(a, b accountMatch) int { return strings.Compare(a.Name, b.Name) })
	labels := accountLabels(matches)
	choices := "  --account " + strings.Join(labels, "\n  --account ")

	if want != "" {
		for _, m := range matches {
			if m.Name == want {
				return m, nil
			}
		}
		return accountMatch{}, fmt.Errorf("no storage account named %q is tagged %q; use one of:\n%s", want, tag, choices)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if pick == nil {
		return accountMatch{}, fmt.Errorf("%d storage accounts are tagged %q; choose one by adding:\n%s", len(matches), tag, choices)
	}
	i, err := pick(fmt.Sprintf("%d storage accounts are tagged %q. Choose one", len(matches), tag), labels)
	if err != nil {
		return accountMatch{}, err
	}
	logf("Tip: skip this question next time with --account %s\n", matches[i].Name)
	return matches[i], nil
}

func accountLabels(matches []accountMatch) []string {
	nameW, subW := 0, 0
	for _, m := range matches {
		nameW = max(nameW, len(m.Name))
		subW = max(subW, len(deref(m.Sub.DisplayName)))
	}
	labels := make([]string, len(matches))
	for i, m := range matches {
		labels[i] = fmt.Sprintf("%-*s  %-*s  %s", nameW, m.Name, subW, deref(m.Sub.DisplayName), m.Location)
	}
	return labels
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
