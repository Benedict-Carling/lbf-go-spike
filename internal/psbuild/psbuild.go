// Package psbuild bundles ps/src into the single lbf.ps1 that Windows installs.
package psbuild

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const petnameModule = "github.com/dustinkirkland/golang-petname"

// Build returns lbf.ps1 for the repository at root, reporting itself as version.
func Build(root, version string) ([]byte, error) {
	if strings.ContainsAny(version, "'\r\n") {
		return nil, fmt.Errorf("version %q cannot be embedded", version)
	}
	srcs, err := filepath.Glob(filepath.Join(root, "ps", "src", "*.ps1"))
	if err != nil {
		return nil, err
	}
	if len(srcs) == 0 {
		return nil, fmt.Errorf("no PowerShell sources in %s", filepath.Join(root, "ps", "src"))
	}
	sort.Strings(srcs)

	bronze, err := os.ReadFile(filepath.Join(root, "profiles", "bronze", "profile.json"))
	if err != nil {
		return nil, err
	}
	browse, err := os.ReadFile(filepath.Join(root, "browse.html"))
	if err != nil {
		return nil, err
	}
	adjectives, names, err := petnameWords(root)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "# lbf %s for PowerShell, built from ps/src in github.com/Benedict-Carling/lbf-cli.\n", version)
	b.WriteString("#Requires -Version 5.1\n")
	fmt.Fprintf(&b, "$script:LbfVersion = '%s'\n", version)
	for _, e := range []struct {
		name string
		raw  []byte
	}{{"EmbeddedBronzeProfile", bronze}, {"EmbeddedBrowseHtml", browse}} {
		text, err := hereString(e.raw)
		if err != nil {
			return nil, fmt.Errorf("embedding %s: %w", e.name, err)
		}
		fmt.Fprintf(&b, "$script:%s = %s\n", e.name, text)
	}
	fmt.Fprintf(&b, "$script:PetnameAdjectives = [string[]]@(%s)\n", psList(adjectives))
	fmt.Fprintf(&b, "$script:PetnameNames = [string[]]@(%s)\n", psList(names))
	for _, src := range srcs {
		raw, err := os.ReadFile(src)
		if err != nil {
			return nil, err
		}
		for i, c := range raw {
			if c > 0x7e || (c < 0x20 && c != '\n' && c != '\r' && c != '\t') {
				return nil, fmt.Errorf("%s: byte %d is 0x%02x; Windows PowerShell 5.1 reads scripts without a BOM as ANSI, so sources must be ASCII", src, i, c)
			}
		}
		fmt.Fprintf(&b, "\n# ---- %s ----\n", filepath.Base(src))
		b.Write(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))
		if !bytes.HasSuffix(raw, []byte("\n")) {
			b.WriteByte('\n')
		}
	}
	return bytes.ReplaceAll(b.Bytes(), []byte("\n"), []byte("\r\n")), nil
}

var marker = regexp.MustCompile(`\{\{U\+[0-9A-F]+\}\}`)

// A readable single-quoted here-string, since Defender's obfuscated-script rule distrusts long base64 strings.
// Non-ASCII characters become {{U+XXXX}}, because Windows PowerShell 5.1 reads a script without a BOM as ANSI.
func hereString(raw []byte) (string, error) {
	s := string(raw)
	if !utf8.ValidString(s) {
		return "", errors.New("not UTF-8")
	}
	if strings.Contains(s, "\r") || marker.MatchString(s) {
		return "", errors.New("contains a carriage return or {{U+...}}, which the embedding cannot carry")
	}
	var b strings.Builder
	for _, r := range s {
		if r > 0x7e || (r < 0x20 && r != '\n' && r != '\t') {
			fmt.Fprintf(&b, "{{U+%04X}}", r)
		} else {
			b.WriteRune(r)
		}
	}
	text := b.String()
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "'@") {
			return "", errors.New("a line starts with '@, which would end the here-string")
		}
	}
	return "[regex]::Replace((@'\n" + text + "\n'@ -replace \"`r`n\", \"`n\"), '\\{\\{U\\+([0-9A-F]+)\\}\\}', { param($m) [char]::ConvertFromUtf32([Convert]::ToInt32($m.Groups[1].Value, 16)) })", nil
}

func psList(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "'" + w + "'"
	}
	return strings.Join(quoted, ",")
}

var wordList = regexp.MustCompile(`(?m)^\s*(adjectives|names)\s*=\s*\[\.\.\.\]string\{([^}]*)\}`)
var word = regexp.MustCompile(`"([a-z]+)"`)

// The two-word lists petname.Generate(2, "-") draws from, read from the module lbf builds with.
func petnameWords(root string) (adjectives, names []string, err error) {
	// go list -m names no directory until the module is downloaded, which a fresh CI runner has not done.
	cmd := exec.Command("go", "mod", "download", "-json", petnameModule)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("downloading %s: %w", petnameModule, err)
	}
	var mod struct{ Dir string }
	if err := json.Unmarshal(out, &mod); err != nil || mod.Dir == "" {
		return nil, nil, fmt.Errorf("finding %s: go mod download printed %q", petnameModule, out)
	}
	src, err := os.ReadFile(filepath.Join(mod.Dir, "petname.go"))
	if err != nil {
		return nil, nil, err
	}
	for _, m := range wordList.FindAllStringSubmatch(string(src), -1) {
		var words []string
		for _, w := range word.FindAllStringSubmatch(m[2], -1) {
			words = append(words, w[1])
		}
		if m[1] == "adjectives" {
			adjectives = words
		} else {
			names = words
		}
	}
	if len(adjectives) == 0 || len(names) == 0 {
		return nil, nil, fmt.Errorf("could not read the word lists from %s's petname.go", petnameModule)
	}
	return adjectives, names, nil
}
