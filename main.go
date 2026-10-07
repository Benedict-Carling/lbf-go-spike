package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	_ "golang.org/x/crypto/x509roots/fallback"
)

const usage = `lbf - land and retrieve datasets in the Azure bronze layer

Usage:
  lbf publish <path>    upload a dataset (alias: upload)
  lbf fetch <id>        download a dataset (alias: download)
  lbf new-id            print a dataset ID to publish under later
  lbf check             prove a credential can publish or fetch, without changing anything
  lbf mint-sas          write a pre-minted credential file
  lbf login             sign in with the browser and remember it
  lbf logout            sign out of lbf and delete its saved tokens
  lbf upgrade           update lbf to the latest release
  lbf version

Run 'lbf <command> --help' for its options.
`

const provenanceHelp = `--derived-from and --instrument are given together or not at all. --provenance is a JSON file:
{"derived_from": "<id>", "instruments": [{"name", "version", "url"}], "properties": {"name": "value"}}.
--profile is a directory holding a JSON Schema profile.json; publish validates against it, and lbf's
bronze profile, before uploading anything.
`

const sasLifetime = 144 * time.Hour

const exitIDTaken = 3

// Set at release build time with -ldflags "-X main.version=v0.1.0".
var version = "dev"

var sasSignature = regexp.MustCompile(`sig=[^&\s"]+`)

func main() {
	// SIGTERM is what HPC schedulers send at a job's time limit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// A second Ctrl-C exits at once instead of waiting for the first to unwind.
	context.AfterFunc(ctx, stop)

	args := os.Args[1:]
	if runtime.GOOS == "windows" {
		args = repairWindowsArgs(args)
	}
	warnIfOutdated := startUpdateCheck(ctx, args)
	err := run(ctx, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", sasSignature.ReplaceAllString(err.Error(), "sig=REDACTED"))
	}
	warnIfOutdated()
	if _, ok := errors.AsType[idTaken](err); ok {
		os.Exit(exitIDTaken)
	}
	if err != nil {
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
		pub, err := preparePublication(positional[0], o.prov, o.profile, o.id)
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
		t, err := resolveTarget(ctx, o.sasEnv, "upload", o.tenant, o.tag, o.account, o.container)
		if err != nil {
			return err
		}
		if err := upload(ctx, t, pub); err != nil {
			return err
		}
		url := t.containerURL() + "/" + pub.ID
		if o.json {
			return printJSON(map[string]string{"id": pub.ID, "url": url})
		}
		fmt.Printf("Uploaded as %s\n%s\n", pub.ID, url)
		return nil

	case "fetch", "download":
		id := positional[0]
		if !validID.MatchString(id) {
			return fmt.Errorf("%q is not a dataset ID", id)
		}
		t, err := resolveTarget(ctx, o.sasEnv, "download", o.tenant, o.tag, o.account, o.container)
		if err != nil {
			return err
		}
		got, err := download(ctx, t, id, o.out)
		if err != nil {
			return err
		}
		if o.json {
			return printJSON(map[string]string{"id": id, "url": t.containerURL() + "/" + id, "path": got.path, "data_path": got.dataPath})
		}
		fmt.Println(got.path)
		return nil

	case "new-id":
		fmt.Println(newID())
		return nil

	case "check":
		if o.mode != "upload" && o.mode != "download" {
			return errors.New("--mode must be 'upload' or 'download'")
		}
		t, err := resolveTarget(ctx, o.sasEnv, o.mode, o.tenant, o.tag, o.account, o.container)
		if err != nil {
			return err
		}
		if err := checkAccess(ctx, t, o.mode); err != nil {
			return err
		}
		fmt.Printf("%s can %s %s/%s until %s\n", orUnknown(t.User), o.mode, t.Account, t.Container, t.Expiry.Format(time.RFC3339))
		return nil

	case "login":
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
		user, hadRecord, hadTokens, err := logout()
		if err != nil {
			return err
		}
		switch {
		case hadTokens:
			fmt.Printf("Signed %s out of lbf and deleted its saved tokens\n", cmp.Or(user, "you"))
		case hadRecord:
			fmt.Printf("Signed %s out of lbf\n", cmp.Or(user, "you"))
		case hasPersistentCache():
			fmt.Println("lbf had no saved login")
		default:
			fmt.Println("This build of lbf never saves a login, so there was nothing to sign out of")
		}
		if user, err := azCLIUser(ctx, o.tenant); err == nil {
			fmt.Printf("Still signed in as %s through 'az login', which lbf uses next; run 'az logout' to sign out of that too\n", user)
		}
		return nil

	case "upgrade":
		if version == "dev" {
			return errors.New("this is a development build; install a release with the command in the README")
		}
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			return err
		}
		latest, upgraded, err := upgrade(ctx, releasesURL, version, exe)
		if err != nil {
			return err
		}
		if upgraded {
			fmt.Printf("Upgraded lbf %s -> %s\n", version, latest)
		} else {
			fmt.Printf("lbf %s is up to date\n", version)
		}
		return nil

	case "mint-sas":
		if o.mode != "upload" && o.mode != "download" {
			return errors.New("--mode must be 'upload' or 'download'")
		}
		t, err := mintTarget(ctx, o.tenant, o.tag, o.account, o.container, o.mode)
		if err != nil {
			return err
		}
		path := o.out
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
	tag, account, tenant, container, sasEnv string
	out, mode                               string
	profile, id                             string
	prov                                    provenanceFlags
	dryRun, json                            bool
}

func parseArgs(cmd string, args []string) (options, []string, error) {
	var o options
	c, err := newCommand(cmd, &o)
	if err != nil {
		return o, nil, err
	}
	positional, err := c.parse(args)
	o.tenant = cmp.Or(o.tenant, imperialTenant)
	return o, positional, err
}

type command struct {
	fs              *flag.FlagSet
	synopsis, notes string
	arg, argDesc    string
	flags           []commandFlag
}

type commandFlag struct {
	name, arg string
	required  bool
}

func newCommand(cmd string, o *options) (*command, error) {
	c := &command{fs: flag.NewFlagSet("lbf "+cmd, flag.ContinueOnError)}
	fs := c.fs
	str := func(p *string, name, arg, def, desc string) {
		fs.StringVar(p, name, def, desc)
		c.flags = append(c.flags, commandFlag{name: name, arg: arg})
	}
	tenant := func() {
		str(&o.tenant, "tenant", "ID", "", "Entra tenant to sign in to (default Imperial College London)")
	}
	storage := func() {
		str(&o.container, "container", "NAME", "bronze", "blob container")
		str(&o.tag, "tag", "KEY=VALUE", "tag=storage", "storage account tag; the test account is tag=storage-test")
		str(&o.account, "account", "NAME", "", "which tagged storage account to use when several are tagged (asked for in a terminal)")
		tenant()
	}
	jsonFlag := func(desc string) {
		fs.BoolVar(&o.json, "json", false, desc)
		c.flags = append(c.flags, commandFlag{name: "json"})
	}
	sasEnv := func() { str(&o.sasEnv, "sas-env", "FILE", "", "pre-minted credential file from 'lbf mint-sas'") }

	switch cmd {
	case "publish", "upload":
		c.synopsis = cmd + " <path> [options]"
		c.arg, c.argDesc = "<path>", "dataset directory to upload"
		c.notes = provenanceHelp
		str(&o.prov.derivedFrom, "derived-from", "ID", "", "the dataset this one was derived from")
		fs.Func("instrument", "what produced it; repeat for several", func(v string) error {
			in, err := parseInstrument(v)
			o.prov.instruments = append(o.prov.instruments, in)
			return err
		})
		c.flags = append(c.flags, commandFlag{name: "instrument", arg: "name=NAME,version=VERSION,url=URL"})
		str(&o.prov.properties, "properties", "FILE", "", `JSON object of property names and values, e.g. {"sample_id": "SAM-0001"}`)
		str(&o.prov.file, "provenance", "FILE", "", "all of the above as one JSON file, instead of those flags")
		str(&o.profile, "profile", "DIR", "", "directory containing profile.json")
		str(&o.id, "id", "ID", "", "publish under this ID from 'lbf new-id'; run again with the same ID to finish a failed publish")
		c.notes += "Exits with 3 when the ID already holds different files or crate, which retrying cannot fix; use a new ID.\n"
		fs.BoolVar(&o.dryRun, "dry-run", false, "validate and print the crate without signing in or uploading")
		c.flags = append(c.flags, commandFlag{name: "dry-run"})
		jsonFlag("print {\"id\", \"url\"} as JSON instead of text")
		storage()
		sasEnv()
	case "fetch", "download":
		c.synopsis = cmd + " <id> [options]"
		c.arg, c.argDesc = "<id>", "dataset ID, as printed by 'lbf publish'"
		str(&o.out, "out", "DIR", ".", "output directory")
		jsonFlag("print {\"id\", \"url\", \"path\", \"data_path\"} as JSON instead of the path; data_path is the uploaded folder inside path")
		storage()
		sasEnv()
	case "new-id":
		c.synopsis = "new-id"
		c.notes = "Prints a new dataset ID without signing in. Pass it to 'lbf publish --id' once the data is ready.\n"
	case "check":
		c.synopsis = "check --mode upload|download [options]"
		c.notes = "Run before a long job, so a missing or expiring credential is found before the work, not after.\n"
		str(&o.mode, "mode", "upload|download", "", "what the credential must be able to do")
		c.flags[len(c.flags)-1].required = true
		storage()
		sasEnv()
	case "mint-sas":
		c.synopsis = "mint-sas --mode upload|download [options]"
		str(&o.mode, "mode", "upload|download", "", "what the credential may do")
		c.flags[len(c.flags)-1].required = true
		str(&o.out, "out", "FILE", "azure_sas.env", "output file")
		storage()
	case "login":
		c.synopsis = "login [options]"
		tenant()
	case "logout":
		c.synopsis = "logout [options]"
		c.notes = "lbf also signs in through 'az login'; logout says if that still signs you in, and 'az logout' ends it.\n"
		tenant()
	case "upgrade":
		c.synopsis = "upgrade"
		c.notes = "lbf also warns after any command when a newer release exists; set LBF_NO_UPDATE_CHECK=1 to stop it checking.\n"
	default:
		return nil, fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	fs.Usage = func() { c.usage(fs.Output()) }
	return c, nil
}

func (c *command) parse(args []string) ([]string, error) {
	positional, err := parseInterspersed(c.fs, args)
	if err != nil {
		return nil, err
	}
	want := 0
	if c.arg != "" {
		want = 1
	}
	if len(positional) > want {
		return nil, c.usageError(fmt.Sprintf("unexpected argument %q", positional[want]))
	}
	if len(positional) < want {
		return nil, c.usageError("missing " + c.arg)
	}
	for _, f := range c.flags {
		if f.required && c.fs.Lookup(f.name).Value.String() == "" {
			return nil, c.usageError("missing --" + f.name)
		}
	}
	return positional, nil
}

func (c *command) usageError(msg string) error {
	var b strings.Builder
	c.usage(&b)
	return fmt.Errorf("%s\n\n%s", msg, b.String())
}

func (c *command) usage(out io.Writer) {
	type row struct{ left, desc string }
	var required, optional []row
	if c.arg != "" {
		required = append(required, row{c.arg, c.argDesc})
	}
	for _, cf := range c.flags {
		f := c.fs.Lookup(cf.name)
		r := row{"--" + strings.TrimSpace(cf.name+" "+cf.arg), f.Usage}
		if f.DefValue != "" && f.DefValue != "false" {
			r.desc += " (default " + f.DefValue + ")"
		}
		if cf.required {
			required = append(required, r)
		} else {
			optional = append(optional, r)
		}
	}
	width := 0
	for _, r := range append(required, optional...) {
		width = max(width, len(r.left))
	}
	fmt.Fprintf(out, "Usage: lbf %s\n", c.synopsis)
	for _, sec := range []struct {
		title string
		rows  []row
	}{{"Required:", required}, {"Optional:", optional}} {
		if len(sec.rows) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n%s\n", sec.title)
		for _, r := range sec.rows {
			fmt.Fprintf(out, "  %-*s   %s\n", width, r.left, r.desc)
		}
	}
	if c.notes != "" {
		fmt.Fprintf(out, "\n%s", c.notes)
	}
}

func printJSON(v any) error {
	return json.NewEncoder(os.Stdout).Encode(v)
}

func resolveTarget(ctx context.Context, sasEnv, mode, tenant, tag, accountName, containerName string) (target, error) {
	if sasEnv != "" {
		return readSASEnv(sasEnv, mode, containerName)
	}
	return mintTarget(ctx, tenant, tag, accountName, containerName, mode)
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
