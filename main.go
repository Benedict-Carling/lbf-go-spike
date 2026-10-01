package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"time"

	_ "golang.org/x/crypto/x509roots/fallback"
)

const usage = `lbf - land and retrieve datasets in the Azure bronze layer

Usage:
  lbf upload <path>       [--tag KEY=VALUE] [--container NAME] [--sas-env FILE]
  lbf download <id>       [--out DIR] [--tag KEY=VALUE] [--container NAME] [--sas-env FILE]
  lbf mint-sas --mode upload|download [--out FILE] [--tag KEY=VALUE] [--container NAME]

Without --sas-env, credentials come from your 'az login' session.
`

const sasLifetime = 144 * time.Hour

var sasSignature = regexp.MustCompile(`sig=[^&\s"]+`)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", sasSignature.ReplaceAllString(err.Error(), "sig=REDACTED"))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return nil
	}

	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	tag := fs.String("tag", "tag=storage", "tag identifying the storage account")
	containerName := fs.String("container", "bronze", "blob container")
	sasEnv := fs.String("sas-env", "", "pre-minted credential file from 'lbf mint-sas'")
	out := fs.String("out", "", "output directory (download) or file (mint-sas)")
	mode := fs.String("mode", "", "mint-sas only: upload or download")

	positional, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return err
	}

	switch args[0] {
	case "upload":
		if len(positional) != 1 {
			return errors.New("usage: lbf upload <path>")
		}
		t, err := resolveTarget(ctx, *sasEnv, "upload", *tag, *containerName)
		if err != nil {
			return err
		}
		id, err := upload(ctx, t, positional[0])
		if err != nil {
			return err
		}
		fmt.Printf("Uploaded as %s\n%s/%s\n", id, t.containerURL(), id)
		return nil

	case "download":
		if len(positional) != 1 {
			return errors.New("usage: lbf download <id>")
		}
		if !validID.MatchString(positional[0]) {
			return fmt.Errorf("%q is not a dataset ID", positional[0])
		}
		t, err := resolveTarget(ctx, *sasEnv, "download", *tag, *containerName)
		if err != nil {
			return err
		}
		dir := *out
		if dir == "" {
			dir = "."
		}
		return download(ctx, t, positional[0], dir)

	case "mint-sas":
		if *mode != "upload" && *mode != "download" {
			return errors.New("--mode must be 'upload' or 'download'")
		}
		t, err := mintTarget(ctx, *tag, *containerName, *mode, sasLifetime)
		if err != nil {
			return err
		}
		path := *out
		if path == "" {
			path = "azure_sas.env"
		}
		if err := writeSASEnv(path, t); err != nil {
			return err
		}
		fmt.Printf("Wrote %s (%s/%s, permissions=%s, expires %s)\n",
			path, t.Account, t.Container, t.Permissions, t.Expiry.Format(time.RFC3339))
		return nil
	}

	return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
}

func resolveTarget(ctx context.Context, sasEnv, mode, tag, containerName string) (target, error) {
	if sasEnv != "" {
		return readSASEnv(sasEnv, mode, containerName)
	}
	return mintTarget(ctx, tag, containerName, mode, sasLifetime)
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
