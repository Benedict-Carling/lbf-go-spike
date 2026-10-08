package main

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	petname "github.com/dustinkirkland/golang-petname"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var (
	validID  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	mintedID = regexp.MustCompile(`^[0-9]{8}-[a-z]+-[a-z]+-[0-9a-f]{4}$`)
)

type localFile struct {
	Path    string
	Rel     string // slash-separated, relative to the upload root: <input name>/...
	Size    int64
	ModTime time.Time
	SHA256  string // set once uploaded
}

func excluded(name string) bool {
	return name == ".DS_Store" || strings.HasPrefix(name, "._")
}

// Mirrors azcopy's layout: a directory lands as <id>/<dirname>/..., a file as <id>/<filename>.
// Symlinks are followed, as cp -RL does, but the dataset takes the name it was given by.
func datasetFiles(input string) (string, []localFile, error) {
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", nil, fmt.Errorf("input %s: %w", input, err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", nil, fmt.Errorf("input %s: %w", input, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	base := filepath.Base(abs)
	source := filepath.Join(parent, base)

	var files []localFile
	var problems []string
	switch {
	case info.IsDir():
		walkDataset(resolved, base, nil, &files, &problems)
	case info.Mode().IsRegular():
		files = []localFile{{Path: resolved, Rel: base, Size: info.Size(), ModTime: info.ModTime()}}
	default:
		problems = append(problems, base+": not a regular file")
	}
	slices.SortFunc(files, func(a, b localFile) int { return strings.Compare(a.Rel, b.Rel) })
	problems = append(problems, checkFiles(files)...)
	if len(problems) > 0 {
		return "", nil, problemList(fmt.Sprintf("%s cannot be published as it is, so nothing was uploaded:", input), problems)
	}
	return source, files, nil
}

// EvalSymlinks does not follow Windows junctions, so loops are found by file identity.
func walkDataset(dir, rel string, ancestors []os.FileInfo, files *[]localFile, problems *[]string) {
	self, err := os.Stat(dir)
	if err == nil && slices.ContainsFunc(ancestors, func(a os.FileInfo) bool { return os.SameFile(a, self) }) {
		*problems = append(*problems, rel+": symlink loop back to a folder above it")
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s: cannot be read (%v)", rel, cause(err)))
		return
	}
	ancestors = append(ancestors, self)
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		r := rel + "/" + e.Name()
		info, err := os.Stat(path)
		switch {
		case err != nil && e.Type()&fs.ModeSymlink != 0:
			dest, _ := os.Readlink(path)
			*problems = append(*problems, fmt.Sprintf("%s: broken symlink to %s", r, dest))
		case err != nil:
			*problems = append(*problems, fmt.Sprintf("%s: %v", r, cause(err)))
		case info.IsDir():
			walkDataset(path, r, ancestors, files, problems)
		case excluded(e.Name()):
		case info.Mode().IsRegular():
			*files = append(*files, localFile{Path: path, Rel: r, Size: info.Size(), ModTime: info.ModTime()})
		default:
			*problems = append(*problems, r+": not a regular file")
		}
	}
}

var windowsReserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³]|conin\$|conout\$)$`)

// Datasets never change once landed, so a name that cannot be fetched everywhere is refused up front.
func checkFiles(files []localFile) []string {
	var problems []string
	if len(files) > 0 && clashesWithCrate(files[0].Rel, true) {
		top, _, _ := strings.Cut(files[0].Rel, "/")
		problems = append(problems, top+": would be replaced by the crate lbf writes; rename it or publish its folder")
	}
	rels := make([]string, len(files))
	for i, f := range files {
		rels[i] = f.Rel
		if p := cmp.Or(encodingProblem(f.Rel), nameProblem(f.Rel)); p != "" {
			problems = append(problems, f.Rel+": "+p)
		}
		fh, err := os.Open(f.Path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: cannot be read (%v)", f.Rel, cause(err)))
			continue
		}
		fh.Close()
	}
	return append(problems, sameNameProblems(rels, "macOS and Windows")...)
}

// Azure's limits on a blob name, which is <id>/<rel>.
func blobNameProblems(id string, files []localFile) []string {
	var problems []string
	for _, f := range files {
		name := id + "/" + f.Rel
		switch {
		case len(name) > 1024:
			problems = append(problems, fmt.Sprintf("%s: too long for Azure, which allows 1024 characters including the dataset ID (this is %d)", f.Rel, len(name)))
		case strings.Count(name, "/")+1 > 254:
			problems = append(problems, fmt.Sprintf("%s: nested too deeply for Azure, which allows 254 path segments including the dataset ID", f.Rel))
		}
	}
	return problems
}

// Linux allows 255 bytes per name, and Azure blob names must be UTF-8.
func encodingProblem(rel string) string {
	if !utf8.ValidString(rel) {
		return "is not valid UTF-8, which Azure blob names must be"
	}
	for seg := range strings.SplitSeq(rel, "/") {
		if len(seg) > 255 {
			return fmt.Sprintf("%q is %d bytes long, but Linux allows at most 255 per name", seg, len(seg))
		}
	}
	return ""
}

func nameProblem(rel string) string {
	for seg := range strings.SplitSeq(rel, "/") {
		stem, _, _ := strings.Cut(seg, ".")
		switch {
		case strings.Contains(seg, `\`):
			return `contains \, which Azure turns into a folder separator`
		case strings.ContainsAny(seg, `:*?"<>|`):
			return `contains one of : * ? " < > |, which Windows cannot store`
		case strings.ContainsFunc(seg, unicode.IsControl):
			return "contains a control character"
		case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
			return fmt.Sprintf("%q ends in a dot or space, which Windows drops", seg)
		case windowsReserved.MatchString(strings.TrimRight(stem, " ")):
			return fmt.Sprintf("%q is a reserved name on Windows", seg)
		}
	}
	return ""
}

// The name macOS and Windows store a path under: one Unicode form, and case folded.
func nameKey(rel string) string {
	return cases.Fold().String(norm.NFC.String(rel))
}

func clashesWithCrate(rel string, fold bool) bool {
	top, _, _ := strings.Cut(rel, "/")
	return top == crateName || fold && nameKey(top) == nameKey(crateName)
}

// Every file and folder must keep its own name where names are case and normalisation insensitive.
func sameNameProblems(rels []string, where string) []string {
	var problems []string
	seen := map[string]string{}
	reported := map[string]bool{}
	for _, rel := range rels {
		for i := 0; i <= len(rel); i++ {
			if i < len(rel) && rel[i] != '/' {
				continue
			}
			path := rel[:i]
			key := nameKey(path)
			other, ok := seen[key]
			if !ok {
				seen[key] = path
				continue
			}
			if other == path {
				continue
			}
			how := "differ only in case"
			if norm.NFC.String(other) == norm.NFC.String(path) {
				how = "are the same name in different Unicode forms"
			}
			if p := fmt.Sprintf("%s and %s %s, so %s cannot hold both", other, path, how, where); !reported[p] {
				reported[p] = true
				problems = append(problems, p)
			}
			break
		}
	}
	return problems
}

func problemList(heading string, problems []string) error {
	const shown = 10
	var b strings.Builder
	b.WriteString(heading)
	for _, p := range problems[:min(len(problems), shown)] {
		b.WriteString("\n  " + p)
	}
	if len(problems) > shown {
		fmt.Fprintf(&b, "\n  ...and %d more", len(problems)-shown)
	}
	return errors.New(b.String())
}

func cause(err error) error {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err
	}
	return err
}

func newID() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s-%s", time.Now().Format("20060102"), petname.Generate(2, "-"), hex.EncodeToString(b))
}

func orUnknown(s string) string {
	return cmp.Or(strings.TrimSpace(s), "unknown")
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
