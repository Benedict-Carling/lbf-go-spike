package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"time"

	_ "golang.org/x/crypto/x509roots/fallback"
)

const usage = `lbf - land and retrieve datasets in the Azure bronze layer

Usage:
  lbf publish <path> [--provenance FILE] [--profile DIR] [--dry-run]   (alias: upload)
  lbf fetch <id> [--out DIR]                                           (alias: download)
  lbf mint-sas --mode upload|download [--out FILE]
  lbf login                                   sign in with the browser and remember it
  lbf logout                                  forget the login saved by 'lbf login'
  lbf version

Common options:
  --container NAME     blob container (default bronze)
  --tag KEY=VALUE      tag identifying the storage account (default tag=storage, production;
                       the test account is tag=storage-test)
  --tenant ID          Entra tenant to sign in to (default Imperial College London)
  --sas-env FILE       pre-minted credential instead of 'az login'

--provenance is a JSON file: {"derived_from": "<id>", "instruments": [{"name", "version", "url"}],
"properties": {"name": "value"}}. --profile is a directory holding a JSON Schema profile.json;
publish validates against it, and lbf's bronze profile, before uploading anything.
`

const sasLifetime = 144 * time.Hour

// Set at release build time with -ldflags "-X main.version=v0.1.0".
var version = "dev"

var sasSignature = regexp.MustCompile(`sig=[^&\s"]+`)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	args := os.Args[1:]
	if runtime.GOOS == "windows" {
		args = repairWindowsArgs(args)
	}
	if err := run(ctx, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", sasSignature.ReplaceAllString(err.Error(), "sig=REDACTED"))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return nil
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Println("lbf", version)
		return nil
	}

	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	tag := fs.String("tag", "tag=storage", "tag identifying the storage account")
	tenant := fs.String("tenant", imperialTenant, "Entra tenant to sign in to")
	containerName := fs.String("container", "bronze", "blob container")
	sasEnv := fs.String("sas-env", "", "pre-minted credential file from 'lbf mint-sas'")
	out := fs.String("out", "", "output directory (fetch) or file (mint-sas)")
	mode := fs.String("mode", "", "mint-sas only: upload or download")
	provenancePath := fs.String("provenance", "", "publish only: provenance JSON file")
	profileDir := fs.String("profile", "", "publish only: directory containing profile.json")
	dryRun := fs.Bool("dry-run", false, "publish only: validate and print the crate without signing in or uploading")

	positional, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return err
	}

	switch args[0] {
	case "publish", "upload":
		if len(positional) != 1 {
			return errors.New("usage: lbf publish <path>")
		}
		pub, err := preparePublication(positional[0], *provenancePath, *profileDir)
		if err != nil {
			return err
		}
		if *dryRun {
			crate, err := pub.crate(target{Account: "dryrun", Container: *containerName, User: "dry-run"})
			if err != nil {
				return err
			}
			fmt.Println(string(crate))
			return nil
		}
		t, err := resolveTarget(ctx, *sasEnv, "upload", *tenant, *tag, *containerName)
		if err != nil {
			return err
		}
		if err := upload(ctx, t, pub); err != nil {
			return err
		}
		fmt.Printf("Uploaded as %s\n%s/%s\n", pub.ID, t.containerURL(), pub.ID)
		return nil

	case "fetch", "download":
		if len(positional) != 1 {
			return errors.New("usage: lbf fetch <id>")
		}
		id := positional[0]
		if !validID.MatchString(id) {
			return fmt.Errorf("%q is not a dataset ID", id)
		}
		t, err := resolveTarget(ctx, *sasEnv, "download", *tenant, *tag, *containerName)
		if err != nil {
			return err
		}
		path, err := download(ctx, t, id, cmp.Or(*out, "."))
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil

	case "login":
		cred, err := login(ctx, *tenant)
		if err != nil {
			return err
		}
		user, err := signedInUser(ctx, cred)
		if err != nil {
			return err
		}
		if hasPersistentCache() {
			fmt.Printf("Signed in as %s; lbf will reuse this login until 'lbf logout'\n", user)
		} else {
			fmt.Printf("Signed in as %s, but this build cannot remember logins; use 'az login' or --sas-env\n", user)
		}
		return nil

	case "logout":
		removed, err := logout()
		if err != nil {
			return err
		}
		if removed {
			fmt.Println("Forgot the login saved by 'lbf login' ('az login' is unaffected)")
		} else {
			fmt.Println("No saved lbf login ('az login' is unaffected)")
		}
		return nil

	case "mint-sas":
		if *mode != "upload" && *mode != "download" {
			return errors.New("--mode must be 'upload' or 'download'")
		}
		t, err := mintTarget(ctx, *tenant, *tag, *containerName, *mode)
		if err != nil {
			return err
		}
		path := cmp.Or(*out, "azure_sas.env")
		if err := writeSASEnv(path, t); err != nil {
			return err
		}
		fmt.Printf("Wrote %s (%s/%s, permissions=%s, expires %s)\n",
			path, t.Account, t.Container, t.Permissions, t.Expiry.Format(time.RFC3339))
		return nil
	}

	return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
}

func resolveTarget(ctx context.Context, sasEnv, mode, tenant, tag, containerName string) (target, error) {
	if sasEnv != "" {
		return readSASEnv(sasEnv, mode, containerName)
	}
	return mintTarget(ctx, tenant, tag, containerName, mode)
}

// PowerShell 5.1 passes 'C:\My Folder\' -x as `C:\My Folder" -x`; a quote cannot appear in a Windows path.
func repairWindowsArgs(args []string) []string {
	var out []string
	for _, a := range args {
		head, rest, found := strings.Cut(a, `"`)
		if !found {
			out = append(out, a)
			continue
		}
		out = append(out, head+`\`)
		out = append(out, strings.Fields(strings.ReplaceAll(rest, `"`, ""))...)
	}
	return out
}

// flag stops at the first positional argument; this lets flags follow it.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
