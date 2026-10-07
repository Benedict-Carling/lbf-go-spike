package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

// Points *f at a temporary file while fn runs and returns what fn wrote there.
func capture(t *testing.T, f **os.File, fn func()) string {
	t.Helper()
	tmp, err := os.Create(filepath.Join(t.TempDir(), "out"))
	must(t, err)
	defer tmp.Close()
	saved := *f
	*f = tmp
	defer func() { *f = saved }()
	fn()
	b, err := os.ReadFile(tmp.Name())
	must(t, err)
	return string(b)
}

// Points os.Stdout at a file that cannot be written while fn runs.
func unwritableStdout(t *testing.T, fn func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ro")
	writeFile(t, path, "")
	ro, err := os.Open(path)
	must(t, err)
	defer ro.Close()
	saved := os.Stdout
	os.Stdout = ro
	defer func() { os.Stdout = saved }()
	fn()
}

func TestEmptyFlagValueIsRefusedNotIgnored(t *testing.T) {
	cases := map[string][]string{
		"publish": {"id", "sas-env", "profile", "properties", "provenance", "container", "tag", "account", "tenant", "derived-from"},
		"fetch":   {"out", "sas-env", "container", "tag", "account", "tenant"},
		"check":   {"mode", "sas-env", "container"},
	}
	pos := map[string][]string{"publish": {"data"}, "fetch": {"20260101-x-y-0000"}, "check": {"--mode", "upload"}}
	for cmd, names := range cases {
		for _, name := range names {
			for _, args := range [][]string{{"--" + name, ""}, {"--" + name + "="}} {
				_, _, err := parseArgs(cmd, append(append([]string{}, pos[cmd]...), args...))
				if err == nil || !strings.HasPrefix(err.Error(), "--"+name+" is empty") {
					t.Errorf("%s %v: got %v", cmd, args, err)
				}
			}
		}
	}
	if _, _, err := parseArgs("publish", []string{"data", "--id", "20260101-x-y-0000", "--container", "silver"}); err != nil {
		t.Errorf("non-empty values: %v", err)
	}
}

func TestSASEnvRefusesFlagsItWouldIgnore(t *testing.T) {
	for _, cmd := range [][]string{{"publish", "data"}, {"fetch", "20260101-x-y-0000"}, {"check", "--mode", "upload"}} {
		for _, extra := range [][]string{
			{"--tag", "tag=storage-test"},
			{"--tag", "tag=storage"},
			{"--account", "testacct"},
			{"--account", "prodacct"},
			{"--tenant", "00000000-0000-0000-0000-000000000000"},
		} {
			args := append(append(append([]string{}, cmd[1:]...), "--sas-env", "azure_sas.env"), extra...)
			_, _, err := parseArgs(cmd[0], args)
			if err == nil || !strings.HasPrefix(err.Error(), extra[0]+" cannot be used with --sas-env") {
				t.Errorf("%s %v: got %v", cmd[0], extra, err)
			}
		}
		args := append(append([]string{}, cmd[1:]...), "--sas-env", "azure_sas.env", "--container", "silver")
		if _, _, err := parseArgs(cmd[0], args); err != nil {
			t.Errorf("%s --sas-env --container: %v", cmd[0], err)
		}
	}
}

func TestNewIDFailsWhenItCannotPrint(t *testing.T) {
	var err error
	unwritableStdout(t, func() { err = run(context.Background(), []string{"new-id"}) })
	if err == nil {
		t.Fatal("new-id succeeded with nowhere to print the ID")
	}
}

func TestDryRunFailsWhenItCannotPrint(t *testing.T) {
	dir := dataset(t, map[string]string{"a.txt": "x"})
	for _, args := range [][]string{{"publish", dir, "--dry-run"}, {"publish", dir, "--dry-run", "--json"}} {
		var err error
		unwritableStdout(t, func() { err = run(context.Background(), args) })
		if err == nil {
			t.Errorf("%v succeeded with nowhere to print", args)
		}
	}
}

func TestPublishedResultLostSaysWhatWasPublished(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		var err error
		unwritableStdout(t, func() {
			err = printPublished(asJSON, "20260101-x-y-0000", "https://a.blob.core.windows.net/bronze/20260101-x-y-0000")
		})
		if err == nil || !strings.Contains(err.Error(), "published 20260101-x-y-0000") || !strings.Contains(err.Error(), "https://a.blob.core.windows.net/bronze/20260101-x-y-0000") {
			t.Errorf("json=%v: %v", asJSON, err)
		}
	}
	for _, asJSON := range []bool{false, true} {
		var err error
		unwritableStdout(t, func() {
			err = printFetched(asJSON, "20260101-x-y-0000", "https://a/bronze/20260101-x-y-0000", fetched{path: "out/20260101-x-y-0000", dataPath: "out/20260101-x-y-0000/run1"})
		})
		if err == nil || !strings.Contains(err.Error(), "fetched 20260101-x-y-0000 into out/20260101-x-y-0000") {
			t.Errorf("json=%v: %v", asJSON, err)
		}
	}
}

func TestDryRunJSONIsPublishShape(t *testing.T) {
	dir := dataset(t, map[string]string{"a.txt": "x"})
	id := newID()
	var err error
	out := capture(t, &os.Stdout, func() {
		err = run(context.Background(), []string{"publish", dir, "--dry-run", "--json", "--id", id, "--container", "silver"})
	})
	must(t, err)
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("want one line, got:\n%s", out)
	}
	var got map[string]string
	must(t, json.Unmarshal([]byte(out), &got))
	want := map[string]string{"id": id, "url": "https://dryrun.blob.core.windows.net/silver/" + id}
	if len(got) != 2 || got["id"] != want["id"] || got["url"] != want["url"] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCommandHelpGoesToStdout(t *testing.T) {
	for _, args := range [][]string{{"publish", "--help"}, {"fetch", "-h"}, {"new-id", "--help"}} {
		var err error
		var stdout string
		stderr := capture(t, &os.Stderr, func() {
			stdout = capture(t, &os.Stdout, func() { err = run(context.Background(), args) })
		})
		if err != nil || !strings.HasPrefix(stdout, "Usage: lbf "+args[0]) || stderr != "" {
			t.Errorf("%v: err %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
		}
	}
}

func TestFlagErrorsPrintOnceWithDoubleDash(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"publish", "data", "--bogus"}, "flag provided but not defined: --bogus"},
		{[]string{"publish", "data", "-bogus"}, "flag provided but not defined: --bogus"},
		{[]string{"publish", "data", "--id"}, "flag needs an argument: --id"},
		{[]string{"publish", "data", "--dry-run=maybe"}, "--dry-run"},
		{[]string{"publish", "data", "--instrument", "nf"}, "for flag --instrument"},
	} {
		var err error
		stderr := capture(t, &os.Stderr, func() { _, _, err = parseArgs(tc.args[0], tc.args[1:]) })
		if stderr != "" {
			t.Errorf("%v printed to stderr itself:\n%s", tc.args, stderr)
		}
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) && !strings.Contains(strings.SplitN(err.Error(), "\n", 2)[0], tc.want) || strings.Count(err.Error(), "Usage: lbf") != 1 {
			t.Errorf("%v: got %v", tc.args, err)
		}
		if first := strings.SplitN(err.Error(), "\n", 2)[0]; regexp.MustCompile(`(^|[^-])-[a-z]`).MatchString(strings.ReplaceAll(first, "dry-run", "")) {
			t.Errorf("%v: single-dash flag in %q", tc.args, first)
		}
	}
}

func TestNoTerminalAccountListCanBePasted(t *testing.T) {
	m := func(name, sub string) accountMatch {
		return accountMatch{Name: name, Location: "uksouth", Sub: &armsubscriptions.Subscription{DisplayName: new(sub)}}
	}
	two := []accountMatch{m("lbfb", "Sub B"), m("lbfa", "Sub A")}
	_, noTerm := chooseAccount(two, "tag=x", "", nil)
	_, unknown := chooseAccount(two, "tag=x", "other", nil)
	for _, err := range []error{noTerm, unknown} {
		if err == nil {
			t.Fatal("no error")
		}
		var flags []string
		for line := range strings.SplitSeq(err.Error(), "\n") {
			if strings.Contains(line, "--account") {
				if !regexp.MustCompile(`^  --account [a-z0-9]+$`).MatchString(line) {
					t.Errorf("not pasteable: %q in\n%s", line, err)
				}
				flags = append(flags, strings.TrimSpace(line))
			}
		}
		if strings.Join(flags, ",") != "--account lbfa,--account lbfb" || !strings.Contains(err.Error(), "Sub B") || !strings.Contains(err.Error(), "uksouth") {
			t.Errorf("incomplete:\n%s", err)
		}
	}
}

func TestPickArrowsClipsWideLabels(t *testing.T) {
	items := []string{strings.Repeat("a", 200), strings.Repeat("b", 200)}
	var out strings.Builder
	got, err := pickArrows(context.Background(), io.MultiReader(strings.NewReader("\x1b[B"), strings.NewReader("\r")), &out, "Choose:", items)
	if err != nil || got != 1 {
		t.Fatalf("got %d %v", got, err)
	}
	width, _ := termSize(&out)
	frames := strings.Split(out.String(), "\x1b[2A")
	if len(frames) != 2 {
		t.Fatalf("want a redraw two rows up, got:\n%q", out.String())
	}
	for _, frame := range frames {
		rows := strings.Split(strings.TrimSuffix(frame, "\r\n\x1b[J"), "\r\n")
		for _, r := range rows[len(rows)-2:] {
			if shown := strings.TrimPrefix(r, "\r\x1b[2K"); len(shown) >= width {
				t.Errorf("row of %d columns on a %d-column terminal", len(shown), width)
			}
		}
	}
}

// Drives the real terminal picker on a pseudo-terminal: python3 opens it, types keys or sends SIGTERM,
// then types "echo-check", which the terminal echoes only if the picker restored it.
const ptyDriver = `
import os, pty, select, signal, sys, time
action, argv = sys.argv[1], sys.argv[2:]
pid, fd = pty.fork()
if pid == 0:
    os.execv(argv[0], argv)
out, acted, checked, deadline = b"", False, False, time.time() + 20
while time.time() < deadline:
    r, _, _ = select.select([fd], [], [], 0.2)
    if r:
        try:
            chunk = os.read(fd, 4096)
        except OSError:
            break
        if not chunk:
            break
        out += chunk
    if not acted and b"Choose" in out:
        acted = True
        time.sleep(0.3)
        if action == "sigterm":
            os.kill(pid, signal.SIGTERM)
        else:
            os.write(fd, action.encode())
    if not checked and b"picked=" in out:
        checked = True
        os.write(fd, b"echo-check")
else:
    os.kill(pid, signal.SIGKILL)
    out += b"\nTIMEOUT"
os.waitpid(pid, 0)
sys.stdout.buffer.write(out)
`

func TestTerminalPickerInATerminal(t *testing.T) {
	if os.Getenv("LBF_PICKER_HELPER") != "" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		i, err := terminalPicker(ctx)("Choose one", []string{"a", "b", "c"})
		fmt.Printf("\npicked=%d err=%v\n", i, err)
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}
	if runtime.GOOS == "windows" {
		t.Skip("needs a Unix pseudo-terminal; check the Windows console by hand")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	for _, tc := range []struct {
		term, action, want string
	}{
		{"dumb", "3\r", "picked=2 err=<nil>"},
		{"dumb", "sigterm", "picked=0 err=context canceled"},
		{"xterm", "\x1b[B\r", "picked=1 err=<nil>"},
		{"xterm", "3", "picked=2 err=<nil>"},
		{"xterm", "sigterm", "picked=0 err=context canceled"},
	} {
		cmd := exec.Command(python, "-I", "-c", ptyDriver, tc.action, os.Args[0], "-test.run=^TestTerminalPickerInATerminal$")
		cmd.Env = append(os.Environ(), "LBF_PICKER_HELPER=1", "TERM="+tc.term)
		out, err := cmd.CombinedOutput()
		_, after, _ := strings.Cut(string(out), tc.want)
		if err != nil || !strings.Contains(after, "echo-check") {
			t.Errorf("TERM=%s %q: want %q, got %v:\n%q", tc.term, tc.action, tc.want, err, out)
		}
		if tc.term == "dumb" && strings.Contains(string(out), "\x1b[") {
			t.Errorf("TERM=dumb got escape codes:\n%q", out)
		}
	}
}

func TestPickersStopWhenCancelled(t *testing.T) {
	for name, pick := range map[string]func(context.Context, io.Reader, io.Writer, string, []string) (int, error){"arrows": pickArrows, "numbered": pickNumbered} {
		ctx, cancel := context.WithCancel(context.Background())
		r, w := io.Pipe()
		done := make(chan error, 1)
		go func() { _, err := pick(ctx, r, io.Discard, "Choose:", []string{"a", "b"}); done <- err }()
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s: got %v", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s: still waiting for a key after cancel", name)
		}
		w.Close()
	}
}

type keyboard struct {
	chunks []string
	reads  int
}

func (c *keyboard) Read(p []byte) (int, error) {
	c.reads++
	if len(c.chunks) == 0 {
		select {}
	}
	n := copy(p, c.chunks[0])
	c.chunks = c.chunks[1:]
	return n, nil
}

func TestPickersLeaveNoReadPending(t *testing.T) {
	for name, tc := range map[string]struct {
		pick func(context.Context, io.Reader, io.Writer, string, []string) (int, error)
		keys []string
	}{"arrows": {pickArrows, []string{"\x1b[B", "\r"}}, "numbered": {pickNumbered, []string{"x\n", "2\n"}}} {
		in := &keyboard{chunks: tc.keys}
		got, err := tc.pick(context.Background(), in, io.Discard, "Choose:", []string{"a", "b"})
		time.Sleep(50 * time.Millisecond)
		if err != nil || got != 1 || in.reads != len(tc.keys) {
			t.Errorf("%s: got %d %v after %d reads, want 1 after %d", name, got, err, in.reads, len(tc.keys))
		}
	}
}

func TestPickArrowsNeverChoosesOnStrayInput(t *testing.T) {
	items := []string{"a", "b", "c"}
	for _, tc := range []struct {
		keys string
		want int
	}{
		{"3", 2},
		{"2\r", 1},
		{"x\r2", 1},
		{"x\r\x1b[B\r", 1},
		{"\x1b[1;2B\r", 1},
	} {
		got, err := pickArrows(context.Background(), strings.NewReader(tc.keys), io.Discard, "Choose:", items)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %d %v, want %d", tc.keys, got, err, tc.want)
		}
	}
	for _, keys := range []string{"x\r", "9\r", "0\r", " \r", "lbfb\r", "\x1b[15~\r"} {
		var out strings.Builder
		if got, err := pickArrows(context.Background(), strings.NewReader(keys), &out, "Choose:", items); !errors.Is(err, errNoChoice) {
			t.Errorf("%q: chose %d %v", keys, got, err)
		}
		if !strings.Contains(out.String(), "type its number") {
			t.Errorf("%q: no hint in %q", keys, out.String())
		}
	}
}
