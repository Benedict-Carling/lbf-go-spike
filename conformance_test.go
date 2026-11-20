package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/ImperialCollegeLondon/lbf-data-tools/lbf/internal/psbuild"
)

// The test binary runs as the Go lbf when re-executed with this set.
const conformanceMainEnv = "LBF_CONFORMANCE_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(conformanceMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The PowerShell lbf runs only when LBF_PS names a PowerShell: pwsh, or powershell.exe for Windows PowerShell 5.1.
func powershell(t *testing.T) string {
	t.Helper()
	ps := os.Getenv("LBF_PS")
	if ps == "" {
		t.Skip("LBF_PS is not set")
	}
	return ps
}

var builtScript = sync.OnceValues(func() (string, error) {
	script, err := psbuild.Build(".", "dev")
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "lbf-ps-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "lbf.ps1")
	return path, os.WriteFile(path, script, 0o644)
})

// A world is one lbf's sandbox: a working directory, a home, and with Azurite a container and a sas-env file for it.
type world struct {
	name      string
	dir       string
	home      string
	container string
	sasEnv    string
	target    target
	env       []string
}

type lbfResult struct {
	stdout, stderr string
	code           int
}

type conformCase struct {
	// Files to create in each world's dir before anything runs, by slash-separated path.
	files map[string]string
	// SAS permissions for a fresh Azurite container per world, e.g. "rlw"; empty for none.
	perms string
	// Runs in each world before the command, e.g. to publish with the Go lbf what a fetch then reads.
	setup func(t *testing.T, w *world)
	// The command line. {dir}, {sas} and {container} are replaced with the world's.
	args []string
	// Unquoted PowerShell text for the arguments, to test what PowerShell itself does to them; replaces args for PowerShell only.
	psRaw string
	env   map[string]string
	// Extra normalizations applied to both lbfs' output, e.g. a generated ID.
	normalize []*regexp.Regexp
	// Compare stderr lines as a set, for parallel transfers that finish in any order.
	unorderedStderr bool
	// Skip comparing files under these slash-separated prefixes of dir.
	ignoreFiles []string
	// Skip comparing stored blobs.
	ignoreBlobs bool
}

func newWorld(t *testing.T, name, perms string, files map[string]string) *world {
	t.Helper()
	// By long name: Windows runners' TEMP is an 8.3 short name, which no real user's folders are.
	root := longPath(t.TempDir())
	w := &world{name: name, dir: filepath.Join(root, "work"), home: filepath.Join(root, "home")}
	must(t, os.MkdirAll(w.dir, 0o755))
	// Windows' known-folder lookup follows USERPROFILE and fails where these do not exist, as they always do on a real profile.
	for _, d := range []string{filepath.Join("AppData", "Local"), filepath.Join("AppData", "Roaming")} {
		must(t, os.MkdirAll(filepath.Join(w.home, d), 0o755))
	}
	for rel, body := range files {
		writeFile(t, filepath.Join(w.dir, filepath.FromSlash(rel)), body)
	}
	w.env = []string{
		"HOME=" + w.home, "USERPROFILE=" + w.home, "XDG_CONFIG_HOME=" + filepath.Join(w.home, ".config"),
		"APPDATA=" + filepath.Join(w.home, "AppData", "Roaming"), "LOCALAPPDATA=" + filepath.Join(w.home, "AppData", "Local"),
		"LBF_NO_UPDATE_CHECK=1", "TERM=dumb",
	}
	if perms != "" {
		var p sas.ContainerPermissions
		p.Read, p.List, p.Write = strings.Contains(perms, "r"), strings.Contains(perms, "l"), strings.Contains(perms, "w")
		tg := emulator(t, p)
		tg.SubscriptionName, tg.SubscriptionID, tg.Permissions = "Emulated", "00000000-0000-0000-0000-000000000000", p.String()
		w.target, w.container = tg, tg.Container
		w.sasEnv = filepath.Join(w.home, "azure_sas.env")
		must(t, writeSASEnv(w.sasEnv, tg))
		w.env = append(w.env, "LBF_BLOB_ENDPOINT="+emulatorURL)
	}
	return w
}

func (w *world) expand(s string) string {
	return strings.NewReplacer("{dir}", w.dir, "{sas}", w.sasEnv, "{container}", w.container).Replace(s)
}

func (w *world) environ(extra map[string]string) []string {
	env := slices.Clone(os.Environ())
	env = append(env, w.env...)
	for k, v := range extra {
		env = append(env, k+"="+w.expand(v))
	}
	return env
}

func runCmd(t *testing.T, cmd *exec.Cmd) lbfResult {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v", cmd.Path, err)
	}
	return lbfResult{out.String(), errb.String(), code}
}

// Runs the Go lbf in w.
func (w *world) lbfGo(t *testing.T, env map[string]string, args ...string) lbfResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	expanded := make([]string, len(args))
	for i, a := range args {
		expanded[i] = w.expand(a)
	}
	cmd := exec.CommandContext(ctx, os.Args[0], expanded...)
	cmd.Dir = w.dir
	cmd.Env = append(w.environ(env), conformanceMainEnv+"=1")
	return runCmd(t, cmd)
}

// Runs lbf.ps1 in w as a user would at a PowerShell prompt, each argument single-quoted unless raw is given.
func (w *world) lbfPS(t *testing.T, env map[string]string, raw string, args ...string) lbfResult {
	t.Helper()
	ps := powershell(t)
	script, err := builtScript()
	must(t, err)
	line := raw
	if line == "" {
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = "'" + psSingleQuotes.Replace(w.expand(a)) + "'"
		}
		line = strings.Join(quoted, " ")
	} else {
		line = w.expand(line)
	}
	// Without this, Windows PowerShell writes "Preparing modules for first use" to a redirected stderr as CLIXML when Set-Location loads its module.
	command := fmt.Sprintf("$ProgressPreference = 'SilentlyContinue'; Set-Location -LiteralPath '%s'; & '%s' %s; exit $LASTEXITCODE", strings.ReplaceAll(w.dir, "'", "''"), script, line)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodeCommand(command))
	cmd.Dir = w.dir
	cmd.Env = w.environ(env)
	return runCmd(t, cmd)
}

// PowerShell ends a single-quoted string at any of the single quotation marks, so each is doubled.
var psSingleQuotes = strings.NewReplacer("'", "''", "\u2018", "\u2018\u2018", "\u2019", "\u2019\u2019", "\u201a", "\u201a\u201a", "\u201b", "\u201b\u201b")

// PowerShell running command; Windows PowerShell would otherwise write module-loading progress to a redirected stderr as CLIXML.
func psCommand(ps, command string) *exec.Cmd {
	return exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodeCommand("$ProgressPreference = 'SilentlyContinue'; "+command))
}

func encodeCommand(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func (w *world) normalize(s string, extra []*regexp.Regexp) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	for _, d := range pathSpellings(w.dir) {
		s = strings.ReplaceAll(s, d, "{dir}")
	}
	for _, h := range pathSpellings(w.home) {
		s = strings.ReplaceAll(s, h, "{home}")
	}
	if w.container != "" {
		s = strings.ReplaceAll(s, w.container, "{container}")
	}
	for _, re := range extra {
		s = re.ReplaceAllString(s, "{"+re.String()+"}")
	}
	return s
}

// Every way an lbf may spell p, longest first: links resolved, Windows' long and short names, either slash, JSON-escaped.
func pathSpellings(p string) []string {
	bases := []string{p, longPath(p)}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		bases = append(bases, real, longPath(real))
	}
	seen := map[string]bool{}
	var out []string
	for _, b := range bases {
		for _, v := range []string{b, filepath.ToSlash(b), strings.ReplaceAll(b, `\`, `\\`)} {
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// The files under w.dir, by slash path, as size and sha256.
func (w *world) files(t *testing.T, ignore []string, extra ...*regexp.Regexp) map[string]string {
	t.Helper()
	got := map[string]string{}
	must(t, filepath.WalkDir(w.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(w.dir, path)
		rel = filepath.ToSlash(rel)
		for _, p := range ignore {
			if rel == p || strings.HasPrefix(rel, p+"/") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			if rel != "." {
				got[rel+"/"] = "dir"
			}
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		norm := w.normalize(string(raw), extra)
		sum := sha256.Sum256([]byte(norm))
		got[rel] = fmt.Sprintf("%d %s", len(norm), hex.EncodeToString(sum[:8]))
		return nil
	}))
	return got
}

// The blobs in w's container: name, size, metadata, and for crates their normalized text.
func (w *world) blobs(t *testing.T, extra []*regexp.Regexp) map[string]string {
	t.Helper()
	if w.container == "" {
		return nil
	}
	cc, err := container.NewClientWithNoCredential(w.target.containerURL()+"?"+w.target.SAS, nil)
	must(t, err)
	got := map[string]string{}
	pager := cc.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{Include: container.ListBlobsInclude{Metadata: true}})
	for pager.More() {
		page, err := pager.NextPage(context.Background())
		must(t, err)
		for _, item := range page.Segment.BlobItems {
			var meta []string
			for k, v := range item.Metadata {
				meta = append(meta, strings.ToLower(k)+"="+deref(v))
			}
			sort.Strings(meta)
			desc := fmt.Sprintf("size=%d %s", deref(item.Properties.ContentLength), strings.Join(meta, " "))
			if strings.HasSuffix(*item.Name, crateName) {
				resp, err := cc.NewBlobClient(*item.Name).DownloadStream(context.Background(), nil)
				must(t, err)
				var b bytes.Buffer
				_, err = b.ReadFrom(resp.Body)
				resp.Body.Close()
				must(t, err)
				body := w.normalize(b.String(), extra)
				desc = fmt.Sprintf("size=%d %s type=%s\n%s", len(body), strings.Join(meta, " "), deref(resp.ContentType), body)
			}
			got[w.normalize(*item.Name, extra)] = desc
		}
	}
	return got
}

var timestampPattern = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})`)

// Runs c through the Go lbf and lbf.ps1 in identical worlds and fails on any difference.
func conform(t *testing.T, c conformCase) {
	t.Helper()
	powershell(t)
	if c.perms != "" {
		requireAzurite(t)
	}
	normalize := append([]*regexp.Regexp{timestampPattern}, c.normalize...)
	results := map[string]lbfResult{}
	worlds := map[string]*world{}
	for _, impl := range []string{"go", "ps"} {
		w := newWorld(t, impl, c.perms, c.files)
		worlds[impl] = w
		if c.setup != nil {
			c.setup(t, w)
		}
		var r lbfResult
		if impl == "go" {
			r = w.lbfGo(t, c.env, c.args...)
		} else {
			r = w.lbfPS(t, c.env, c.psRaw, c.args...)
		}
		r.stdout = w.normalize(r.stdout, normalize)
		r.stderr = w.normalize(r.stderr, normalize)
		if c.unorderedStderr {
			lines := strings.Split(r.stderr, "\n")
			sort.Strings(lines)
			r.stderr = strings.Join(lines, "\n")
		}
		results[impl] = r
	}
	g, p := results["go"], results["ps"]
	if g.code != p.code {
		t.Errorf("exit code: go %d, ps %d", g.code, p.code)
	}
	if g.stdout != p.stdout {
		t.Errorf("stdout differs:\n%s", lineDiff(g.stdout, p.stdout))
	}
	if g.stderr != p.stderr {
		t.Errorf("stderr differs:\n%s", lineDiff(g.stderr, p.stderr))
	}
	gf, pf := worlds["go"].files(t, c.ignoreFiles, normalize...), worlds["ps"].files(t, c.ignoreFiles, normalize...)
	if d := mapDiff(gf, pf); d != "" {
		t.Errorf("files differ:\n%s", d)
	}
	if !c.ignoreBlobs {
		gb, pb := worlds["go"].blobs(t, normalize), worlds["ps"].blobs(t, normalize)
		if d := mapDiff(gb, pb); d != "" {
			t.Errorf("blobs differ:\n%s", d)
		}
	}
	if t.Failed() {
		t.Logf("go stdout:\n%s\ngo stderr:\n%s", g.stdout, g.stderr)
		t.Logf("ps stdout:\n%s\nps stderr:\n%s", p.stdout, p.stderr)
	}
}

func requireAzurite(t *testing.T) {
	t.Helper()
	emulator(t, sas.ContainerPermissions{Read: true})
}

func lineDiff(goText, psText string) string {
	g, p := strings.Split(goText, "\n"), strings.Split(psText, "\n")
	var b strings.Builder
	for i := 0; i < max(len(g), len(p)); i++ {
		var gl, pl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(p) {
			pl = p[i]
		}
		if gl != pl {
			fmt.Fprintf(&b, "  line %d\n    go: %q\n    ps: %q\n", i+1, gl, pl)
		}
	}
	return b.String()
}

func mapDiff(g, p map[string]string) string {
	keys := map[string]bool{}
	for k := range g {
		keys[k] = true
	}
	for k := range p {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var b strings.Builder
	for _, k := range sorted {
		gv, gok := g[k]
		pv, pok := p[k]
		switch {
		case !gok:
			fmt.Fprintf(&b, "  %s: only ps: %s\n", k, pv)
		case !pok:
			fmt.Fprintf(&b, "  %s: only go: %s\n", k, gv)
		case gv != pv:
			fmt.Fprintf(&b, "  %s:\n%s", k, lineDiff(gv, pv))
		}
	}
	return b.String()
}

func TestConformanceCLI(t *testing.T) {
	cases := map[string]conformCase{
		"no arguments":          {args: nil},
		"help":                  {args: []string{"--help"}},
		"help word":             {args: []string{"help"}},
		"version":               {args: []string{"version"}},
		"unknown command":       {args: []string{"frobnicate"}},
		"unknown flag":          {args: []string{"fetch", "--nope", "x"}},
		"bad flag syntax":       {args: []string{"publish", "---x"}},
		"flag needs argument":   {args: []string{"publish", "data", "--name"}},
		"bad boolean":           {args: []string{"publish", "data", "--json=maybe"}},
		"missing argument":      {args: []string{"fetch"}},
		"extra argument":        {args: []string{"fetch", "a", "b"}},
		"missing required flag": {args: []string{"check"}},
		"empty flag value":      {args: []string{"fetch", "--out", " ", "x"}},
		"sas-env with tag":      {args: []string{"check", "--mode", "upload", "--sas-env", "x", "--tag", "a=b"}},
		"sas-env with tenant":   {args: []string{"check", "--mode", "upload", "--sas-env", "x", "--tenant", "t"}},
		"bad mode":              {args: []string{"check", "--mode", "sideways", "--sas-env", "x"}},
		"fetch bad id":          {args: []string{"fetch", "../x"}},
		"upgrade dev build":     {args: []string{"upgrade"}},
		"single dash help":      {args: []string{"new-id", "-h"}},
		"numbers typed bare":    {args: []string{"fetch", "0012", "1.50"}, psRaw: "fetch 0012 1.50"},
		"comma list typed bare": {args: []string{"fetch", "a,b", "--nope"}, psRaw: "fetch a,b --nope"},
		"double dash":           {args: []string{"fetch", "--", "-x"}, psRaw: "fetch -- -x"},
	}
	for _, cmd := range []string{"publish", "upload", "fetch", "download", "browse", "new-id", "check", "mint-sas", "login", "logout", "upgrade"} {
		cases["help for "+cmd] = conformCase{args: []string{cmd, "--help"}}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			conform(t, c)
		})
	}
}

func TestConformanceNewID(t *testing.T) {
	powershell(t)
	w := newWorld(t, "ps", "", nil)
	seen := map[string]bool{}
	for range 3 {
		r := w.lbfPS(t, nil, "", "new-id")
		id := strings.TrimSpace(strings.ReplaceAll(r.stdout, "\r\n", "\n"))
		if r.code != 0 || !regexp.MustCompile(`^`+time.Now().Format("20060102")+`-[a-z]+-[a-z]+-[0-9a-f]{4}$`).MatchString(id) {
			t.Fatalf("new-id printed %q (exit %d, stderr %q)", r.stdout, r.code, r.stderr)
		}
		seen[id] = true
	}
	if len(seen) < 2 {
		t.Errorf("new-id repeated itself: %v", seen)
	}
}
