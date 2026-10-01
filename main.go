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

	o, positional, err := parseArgs(args[0], args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}

	switch args[0] {
	case "publish", "upload":
		if len(positional) != 1 {
			return errors.New("usage: lbf publish <path>")
		}
		pub, err := preparePublication(positional[0], o.provenance, o.profile)
		if err != nil {
			return err
		}
		if o.dryRun {
			crate, err := pub.crate(target{Account: "dryrun", Container: o.container, User: "dry-run"})
			if err != nil {
				return err
			}
			fmt.Println(string(crate))
			return nil
		}
		t, err := resolveTarget(ctx, o.sasEnv, "upload", o.tenant, o.tag, o.container)
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
		t, err := resolveTarget(ctx, o.sasEnv, "download", o.tenant, o.tag, o.container)
		if err != nil {
			return err
		}
		path, err := download(ctx, t, id, cmp.Or(o.out, "."))
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil

	case "login":
		if len(positional) != 0 {
			return errors.New("usage: lbf login [--tenant ID]")
		}
		cred, err := login(ctx, o.tenant)
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
		if len(positional) != 0 {
			return errors.New("usage: lbf logout")
		}
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
		if len(positional) != 0 {
			return errors.New("usage: lbf mint-sas --mode upload|download [--out FILE]")
		}
		if o.mode != "upload" && o.mode != "download" {
			return errors.New("--mode must be 'upload' or 'download'")
		}
		t, err := mintTarget(ctx, o.tenant, o.tag, o.container, o.mode)
		if err != nil {
			return err
		}
		path := cmp.Or(o.out, "azure_sas.env")
		if err := writeSASEnv(path, t); err != nil {
			return err
		}
		fmt.Printf("Wrote %s (%s/%s, permissions=%s, expires %s)\n",
			path, t.Account, t.Container, t.Permissions, t.Expiry.Format(time.RFC3339))
		return nil
	}
	return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
}

type options struct {
	tag, tenant, container, sasEnv string
	out, mode                      string
	provenance, profile            string
	dryRun                         bool
}

func parseArgs(cmd string, args []string) (options, []string, error) {
	var o options
	fs := flag.NewFlagSet("lbf "+cmd, flag.ContinueOnError)
	tenant := func() { fs.StringVar(&o.tenant, "tenant", imperialTenant, "Entra tenant to sign in to") }
	storage := func() {
		tenant()
		fs.StringVar(&o.tag, "tag", "tag=storage", "tag identifying the storage account")
		fs.StringVar(&o.container, "container", "bronze", "blob container")
	}
	sasEnv := func() { fs.StringVar(&o.sasEnv, "sas-env", "", "pre-minted credential file from 'lbf mint-sas'") }

	switch cmd {
	case "publish", "upload":
		storage()
		sasEnv()
		fs.StringVar(&o.provenance, "provenance", "", "provenance JSON file")
		fs.StringVar(&o.profile, "profile", "", "directory containing profile.json")
		fs.BoolVar(&o.dryRun, "dry-run", false, "validate and print the crate without signing in or uploading")
	case "fetch", "download":
		storage()
		sasEnv()
		fs.StringVar(&o.out, "out", "", "output directory")
	case "mint-sas":
		storage()
		fs.StringVar(&o.out, "out", "", "output file (default azure_sas.env)")
		fs.StringVar(&o.mode, "mode", "", "upload or download")
	case "login":
		tenant()
	case "logout":
	default:
		return o, nil, fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	positional, err := parseInterspersed(fs, args)
	return o, positional, err
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
