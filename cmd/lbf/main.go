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
  lbf browse            search and look through every dataset in your browser
  lbf check             prove a credential can publish or fetch, without changing anything
  lbf mint-sas          write a pre-minted credential file
  lbf login             sign in with the browser and remember it
  lbf logout            sign out of lbf and delete its saved tokens
  lbf upgrade           update lbf to the latest release
  lbf version

Run 'lbf <command> --help' for its options.
`

const provenanceHelp = `--derived-from and --instrument are given together or not at all. --provenance is a JSON file:
{"name", "description", "derived_from": "<id>", "instruments": [{"name", "version", "url"}], "properties": {"name": "value"}};
--property, --name and --description add to it, and anything given twice is refused.
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

	args := commandArgs()
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
		c, _ := newCommand(args[0], new(options))
		c.usage(os.Stdout)
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
			t := target{Account: "dryrun", Container: o.container, User: "dry-run"}
			crate, err := pub.crate(t)
			if err != nil {
				return err
			}
			if o.json {
				err = printJSON(map[string]string{"id": pub.ID, "url": t.containerURL() + "/" + pub.ID})
			} else {
				_, err = fmt.Println(string(crate))
			}
			if err != nil {
				return fmt.Errorf("could not write the dry run's result to stdout: %w", err)
			}
			return nil
		}
		t, err := o.target(ctx, "upload", o.container)
		if err != nil {
			return err
		}
		if err := upload(ctx, t, pub); err != nil {
			return err
		}
		return printPublished(o.json, pub.ID, t.containerURL()+"/"+pub.ID)

	case "fetch", "download":
		id := positional[0]
		if !validID.MatchString(id) {
			return fmt.Errorf("%q is not a dataset ID", id)
		}
		t, err := o.target(ctx, "download", o.container)
		if err != nil {
			return err
		}
		got, err := download(ctx, t, id, o.out)
		if err != nil {
			return err
		}
		return printFetched(o.json, id, t.containerURL()+"/"+id, got)

	case "new-id":
		if _, err := fmt.Println(newID()); err != nil {
			return fmt.Errorf("could not write the new ID to stdout: %w", err)
		}
		return nil

	case "browse":
		return browse(ctx, o)

	case "check":
		if o.mode != "upload" && o.mode != "download" {
			return errors.New("--mode must be 'upload' or 'download'")
		}
		t, err := o.target(ctx, o.mode, o.container)
		if err != nil {
			return err
		}
		if err := checkAccess(ctx, t, o.mode); err != nil {
			return err
		}
		fmt.Println(checkSummary(t, o.mode))
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
		_, storeErr := tokenCache()
		fmt.Println(loginMessage(user, storeErr))
		return nil

	case "logout":
		user, hadRecord, hadTokens, err := logout()
		if err != nil {
			return err
		}
		_, storeErr := tokenCache()
		fmt.Println(logoutMessage(user, hadRecord, hadTokens, storeErr))
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

// Without --sas-env the expiry is that of a throwaway SAS, not of the sign-in a later command uses.
func checkSummary(t target, mode string) string {
	if t.Source == "" {
		return fmt.Sprintf("%s can %s %s/%s until %s", orUnknown(t.User), mode, t.Account, t.Container, t.Expiry.Format(time.RFC3339))
	}
	return fmt.Sprintf("%s can %s %s/%s, signed in via %s", orUnknown(t.User), mode, t.Account, t.Container, t.Source)
}

func loginMessage(user string, storeErr error) string {
	switch {
	case storeErr == nil:
		return fmt.Sprintf("Signed in as %s; lbf will reuse this login until 'lbf logout'", user)
	case errors.Is(storeErr, errNoCredentialStore):
		return fmt.Sprintf("Signed in as %s, but this build cannot remember logins; use 'az login' or --sas-env", user)
	default:
		return fmt.Sprintf("Signed in as %s, but lbf could not remember this login because %v; use 'az login' or --sas-env", user, storeErr)
	}
}

func logoutMessage(user string, hadRecord, hadTokens bool, storeErr error) string {
	switch {
	case hadTokens:
		return fmt.Sprintf("Signed %s out of lbf and deleted its saved tokens", cmp.Or(user, "you"))
	case hadRecord:
		return fmt.Sprintf("Signed %s out of lbf", cmp.Or(user, "you"))
	case errors.Is(storeErr, errNoCredentialStore):
		return "This build of lbf never saves a login, so there was nothing to sign out of"
	default:
		return "lbf had no saved login"
	}
}

type options struct {
	tag, account, tenant, container, sasEnv string
	out, mode, port                         string
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
	name, arg     string
	required, str bool
}

func newCommand(cmd string, o *options) (*command, error) {
	c := &command{fs: flag.NewFlagSet("lbf "+cmd, flag.ContinueOnError)}
	fs := c.fs
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	str := func(p *string, name, arg, def, desc string) {
		fs.StringVar(p, name, def, desc)
		c.flags = append(c.flags, commandFlag{name: name, arg: arg, str: true})
	}
	tenant := func() {
		str(&o.tenant, "tenant", "ID", "", "Entra tenant to sign in to (default Imperial College London)")
	}
	account := func() {
		str(&o.tag, "tag", "KEY=VALUE", "tag=storage", "storage account tag; the test account is tag=storage-test")
		str(&o.account, "account", "NAME", "", "which tagged storage account to use when several are tagged (asked for in a terminal)")
		tenant()
	}
	storage := func() {
		str(&o.container, "container", "NAME", "bronze", "blob container")
		account()
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
		fs.Func("property", "a property of the dataset; repeat for several", func(v string) error {
			o.prov.props = append(o.prov.props, v)
			return nil
		})
		c.flags = append(c.flags, commandFlag{name: "property", arg: "NAME=VALUE"})
		str(&o.prov.properties, "properties", "FILE", "", `JSON object of property names and values, e.g. {"sample_id": "SAM-0001"}`)
		str(&o.prov.name, "name", "TEXT", "", "what people call the dataset (default the folder's name)")
		str(&o.prov.description, "description", "TEXT", "", "what the dataset holds (default one lbf writes)")
		str(&o.prov.file, "provenance", "FILE", "", "the dataset's name, description, parent, instruments and properties as one JSON file")
		str(&o.profile, "profile", "DIR", "", "directory containing profile.json")
		str(&o.id, "id", "ID", "", "publish under this ID from 'lbf new-id'; run again with the same ID to finish a failed publish")
		c.notes += "Exits with 3 when the ID already holds different files or provenance, which retrying cannot fix; use a new ID.\n"
		fs.BoolVar(&o.dryRun, "dry-run", false, "validate and print the crate without signing in or uploading")
		c.flags = append(c.flags, commandFlag{name: "dry-run"})
		jsonFlag("print {\"id\", \"url\"} as JSON instead of text, or instead of the crate with --dry-run (its url names the placeholder account dryrun)")
		storage()
		sasEnv()
	case "fetch", "download":
		c.synopsis = cmd + " <id> [options]"
		c.arg, c.argDesc = "<id>", "dataset ID, as printed by 'lbf publish'"
		str(&o.out, "out", "DIR", ".", "output directory")
		jsonFlag("print {\"id\", \"url\", \"path\", \"data_path\", \"provenance\"} as JSON instead of the path; data_path is the uploaded folder inside path, and provenance what its crate states, as in a --provenance file")
		storage()
		sasEnv()
	case "browse":
		c.synopsis = "browse [options]"
		c.notes = "Reads every dataset's crate, caching each one since crates never change, and serves them on this computer only.\n"
		str(&o.container, "container", "NAMES", "", "comma-separated containers to read (default every container you can list)")
		str(&o.port, "port", "PORT", "0", "local port to serve on (0 picks a free one)")
		account()
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
	return c, nil
}

var flagErrorName = regexp.MustCompile(`^(flag provided but not defined: |flag needs an argument: |invalid value ".*" for flag |invalid boolean value ".*" for )-`)

func (c *command) parse(args []string) ([]string, error) {
	positional, err := parseInterspersed(c.fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil, err
	}
	if err != nil {
		return nil, c.usageError(flagErrorName.ReplaceAllString(err.Error(), "$1--"))
	}
	set := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for _, f := range c.flags {
		if f.str && set[f.name] && strings.TrimSpace(c.fs.Lookup(f.name).Value.String()) == "" {
			return nil, fmt.Errorf("--%s is empty; give it a value, or leave it out for the default", f.name)
		}
	}
	if set["sas-env"] {
		for _, name := range []string{"tag", "account"} {
			if set[name] {
				return nil, fmt.Errorf("--%s cannot be used with --sas-env, whose file already names the storage account; choose the account when minting the file with 'lbf mint-sas'", name)
			}
		}
		if set["tenant"] {
			return nil, errors.New("--tenant cannot be used with --sas-env, which needs no sign-in")
		}
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

func printPublished(asJSON bool, id, url string) error {
	var err error
	if asJSON {
		err = printJSON(map[string]string{"id": id, "url": url})
	} else {
		_, err = fmt.Printf("Uploaded as %s\n%s\n", id, url)
	}
	if err != nil {
		return fmt.Errorf("published %s at %s, but could not write that to stdout: %w", id, url, err)
	}
	return nil
}

func printFetched(asJSON bool, id, url string, got fetched) error {
	var err error
	if asJSON {
		err = printJSON(map[string]any{"id": id, "url": url, "path": got.path, "data_path": got.dataPath, "provenance": got.stated})
	} else {
		_, err = fmt.Println(got.path)
	}
	if err != nil {
		return fmt.Errorf("fetched %s into %s, but could not write that to stdout: %w", id, got.path, err)
	}
	return nil
}

// The storage account and container the options pick, from --sas-env or by signing in.
func (o options) target(ctx context.Context, mode, container string) (target, error) {
	if o.sasEnv != "" {
		return readSASEnv(o.sasEnv, mode, container)
	}
	return mintTarget(ctx, o.tenant, o.tag, o.account, container, mode)
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
