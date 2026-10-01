package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Same file format as azure-mint-sas.nf, so either tool can read the other's credentials.
var sasEnvKeys = []string{
	"AZURE_ACCOUNT", "AZURE_CONTAINER", "AZURE_SAS", "AZURE_SAS_EXPIRY",
	"AZURE_SAS_PERMISSIONS", "USER_IDENTITY", "SUBSCRIPTION_NAME", "SUBSCRIPTION_ID",
}

var (
	sasEnvLine  = regexp.MustCompile(`^([A-Z_]+)='([^']*)'[ \t]*$`)
	accountName = regexp.MustCompile(`^[a-z0-9]{3,24}$`)
)

func writeSASEnv(path string, t target) error {
	values := map[string]string{
		"AZURE_ACCOUNT":         t.Account,
		"AZURE_CONTAINER":       t.Container,
		"AZURE_SAS":             t.SAS,
		"AZURE_SAS_EXPIRY":      t.Expiry.UTC().Format(time.RFC3339),
		"AZURE_SAS_PERMISSIONS": t.Permissions,
		"USER_IDENTITY":         t.User,
		"SUBSCRIPTION_NAME":     t.SubscriptionName,
		"SUBSCRIPTION_ID":       t.SubscriptionID,
	}
	var b strings.Builder
	for _, k := range sasEnvKeys {
		if strings.ContainsAny(values[k], "'\n\r") {
			return fmt.Errorf("%s contains a character the credential file cannot hold", k)
		}
		fmt.Fprintf(&b, "%s='%s'\n", k, values[k])
	}
	// On Windows the mode only controls read-only; the file is private by living in the user's profile.
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func readSASEnv(path, mode, expectContainer string) (target, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return target{}, err
	}
	remedy := fmt.Sprintf("mint a fresh one with: lbf mint-sas --mode %s", mode)

	allowed := map[string]bool{}
	for _, k := range sasEnvKeys {
		allowed[k] = true
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := sasEnvLine.FindStringSubmatch(line)
		if m == nil {
			return target{}, fmt.Errorf("%s: not a KEY='value' line: %.60q; %s", path, line, remedy)
		}
		if !allowed[m[1]] {
			return target{}, fmt.Errorf("%s: unexpected key %s; %s", path, m[1], remedy)
		}
		if _, dup := values[m[1]]; dup {
			return target{}, fmt.Errorf("%s: %s appears more than once; %s", path, m[1], remedy)
		}
		values[m[1]] = m[2]
	}
	for _, k := range sasEnvKeys {
		if values[k] == "" {
			return target{}, fmt.Errorf("%s is missing a value for %s; %s", path, k, remedy)
		}
	}

	if !accountName.MatchString(values["AZURE_ACCOUNT"]) {
		return target{}, fmt.Errorf("%s has an invalid storage account name %q; %s", path, values["AZURE_ACCOUNT"], remedy)
	}
	expiry, err := time.Parse(time.RFC3339, values["AZURE_SAS_EXPIRY"])
	if err != nil {
		return target{}, fmt.Errorf("%s has an unreadable AZURE_SAS_EXPIRY; %s", path, remedy)
	}
	if time.Now().After(expiry) {
		return target{}, fmt.Errorf("the SAS in %s expired at %s; %s", path, values["AZURE_SAS_EXPIRY"], remedy)
	}
	if mode == "upload" && !strings.Contains(values["AZURE_SAS_PERMISSIONS"], "w") {
		return target{}, fmt.Errorf("%s grants %q and cannot be used to upload; re-mint with --mode upload", path, values["AZURE_SAS_PERMISSIONS"])
	}
	if values["AZURE_CONTAINER"] != expectContainer {
		return target{}, fmt.Errorf("%s targets container %q, but this run asked for %q", path, values["AZURE_CONTAINER"], expectContainer)
	}

	logf("[sas] using %s -> %s/%s (permissions=%s, expires %s)\n",
		path, values["AZURE_ACCOUNT"], values["AZURE_CONTAINER"], values["AZURE_SAS_PERMISSIONS"], values["AZURE_SAS_EXPIRY"])

	return target{
		Account:          values["AZURE_ACCOUNT"],
		Container:        values["AZURE_CONTAINER"],
		SAS:              values["AZURE_SAS"],
		Expiry:           expiry,
		Permissions:      values["AZURE_SAS_PERMISSIONS"],
		User:             values["USER_IDENTITY"],
		SubscriptionName: values["SUBSCRIPTION_NAME"],
		SubscriptionID:   values["SUBSCRIPTION_ID"],
	}, nil
}
