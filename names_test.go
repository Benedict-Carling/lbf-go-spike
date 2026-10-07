package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func readableFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x")
	writeFile(t, path, "x")
	return path
}

func filesAt(path string, rels ...string) []localFile {
	files := make([]localFile, len(rels))
	for i, r := range rels {
		files[i] = localFile{Path: path, Rel: r}
	}
	return files
}

func crateFilesAt(rels ...string) []crateFile {
	files := make([]crateFile, len(rels))
	for i, r := range rels {
		files[i].Rel = r
	}
	return files
}

func wantOneProblem(t *testing.T, name string, problems []string, wants ...string) {
	t.Helper()
	if len(problems) != 1 {
		t.Errorf("%s: want one problem, got %q", name, problems)
		return
	}
	for _, w := range wants {
		if !strings.Contains(problems[0], w) {
			t.Errorf("%s: want %q in %q", name, w, problems[0])
		}
	}
}

func TestCaseVariantOfTheCrateNameIsRefused(t *testing.T) {
	ok := readableFile(t)
	for _, rel := range []string{"RO-CRATE-METADATA.JSON", "Ro-Crate-Metadata.json/a.txt"} {
		wantOneProblem(t, rel, checkFiles(filesAt(ok, rel)), "replaced by the crate")
	}
	if got := checkFiles(filesAt(ok, "run1/RO-CRATE-METADATA.JSON")); len(got) != 0 {
		t.Errorf("a crate-like name inside the published folder: %q", got)
	}

	for _, goos := range []string{"darwin", "windows"} {
		wantOneProblem(t, goos, localNameProblems(crateFilesAt("RO-CRATE-METADATA.JSON"), goos), "RO-CRATE-METADATA.JSON", "crate")
	}
	if got := localNameProblems(crateFilesAt("RO-CRATE-METADATA.JSON"), "linux"); len(got) != 0 {
		t.Errorf("linux keeps both: %q", got)
	}
	wantOneProblem(t, "linux exact", localNameProblems(crateFilesAt(crateName), "linux"), crateName)
}

func TestNamesThatLandAsOneAreRefused(t *testing.T) {
	ok := readableFile(t)
	nfc, nfd := "run1/caf\u00e9.txt", "run1/cafe\u0301.txt"
	for name, pair := range map[string][2]string{
		"unicode forms":        {nfc, nfd},
		"final sigma":          {"run1/σ.txt", "run1/ς.txt"},
		"file and folder":      {"run1/Data", "run1/data/x.txt"},
		"folders":              {"run1/A/x.txt", "run1/a/y.txt"},
		"case, as before":      {"run1/Same.txt", "run1/same.txt"},
		"folder of a folder":   {"run1/X/deep/a.txt", "run1/x/deep/b.txt"},
		"file inside a folder": {"run1/A/x.txt", "run1/a/x.txt"},
	} {
		first, second := pair[0], pair[1]
		if first > second {
			first, second = second, first
		}
		problems := checkFiles(filesAt(ok, first, second))
		wantOneProblem(t, name, problems, "macOS and Windows")
		for _, goos := range []string{"darwin", "windows"} {
			wantOneProblem(t, name+" on "+goos, localNameProblems(crateFilesAt(first, second), goos), "this computer")
		}
		if got := localNameProblems(crateFilesAt(first, second), "linux"); len(got) != 0 {
			t.Errorf("%s on linux: %q", name, got)
		}
	}

	problems := checkFiles(filesAt(ok, "run1/A/x.txt", "run1/a/y.txt", "run1/a/z.txt"))
	wantOneProblem(t, "one report per pair", problems, "run1/A and run1/a")
	problems = checkFiles(filesAt(ok, "run1/Data", "run1/data/x.txt"))
	wantOneProblem(t, "file and folder named", problems, "run1/Data and run1/data")
	problems = checkFiles(filesAt(ok, nfc, nfd))
	wantOneProblem(t, "unicode forms named", problems, nfc+" and "+nfd)

	if got := checkFiles(filesAt(ok, "run1/a/x.txt", "run1/a/y.txt", "run1/b.txt")); len(got) != 0 {
		t.Errorf("distinct names: %q", got)
	}
}

func TestEveryWindowsReservedNameIsRefused(t *testing.T) {
	for _, seg := range []string{
		"CON", "con.txt", "nul .txt", "NUL  .tar.gz", "aux", "PRN.csv", "COM1", "lpt9.log",
		"CONIN$", "conout$.txt", "COM¹", "com².txt", "COM³", "LPT¹", "lpt²", "LPT³.dat",
	} {
		if p := nameProblem("run1/" + seg); !strings.Contains(p, "reserved name on Windows") {
			t.Errorf("%q: %q", seg, p)
		}
	}
	for _, seg := range []string{"console.txt", "nully", "COM10", "com0", "LPT", "conin", "my nul.txt", "aux_data"} {
		if p := nameProblem("run1/" + seg); p != "" {
			t.Errorf("%q flagged: %q", seg, p)
		}
	}
	wantOneProblem(t, "fetch on windows", localNameProblems(crateFilesAt("run1/CONIN$"), "windows"), "run1/CONIN$")
}

func TestNamesLinuxOrAzureCannotHoldAreRefused(t *testing.T) {
	ok := readableFile(t)
	long := "run1/" + strings.Repeat("文", 200) + ".csv"
	wantOneProblem(t, "long segment", checkFiles(filesAt(ok, long)), "255")
	if got := checkFiles(filesAt(ok, "run1/"+strings.Repeat("文", 85))); len(got) != 0 {
		t.Errorf("255-byte segment: %q", got)
	}
	wantOneProblem(t, "invalid UTF-8", checkFiles(filesAt(ok, "run1/caf\xe9.txt")), "UTF-8")
	if got := localNameProblems(crateFilesAt(long), "windows"); len(got) != 0 {
		t.Errorf("fetch of a long name on windows: %q", got)
	}
}

func TestBlobNamesAzureCannotHoldAreRefused(t *testing.T) {
	ok := readableFile(t)
	id := "20260101-x-y-0000"
	room := 1024 - len(id) - 1
	fits := "run1/" + strings.Repeat(strings.Repeat("c", 200)+"/", 4)
	fits += strings.Repeat("c", room-len(fits))
	if got := blobNameProblems(id, filesAt(ok, fits)); len(got) != 0 {
		t.Errorf("a 1024-character blob name: %q", got)
	}
	tooLong := "run1/" + strings.Repeat("abcdefghij/", 101) + "x"
	wantOneProblem(t, "long", blobNameProblems(id, filesAt(ok, tooLong)), "1024")
	deep := "run1/" + strings.Repeat("d/", 252) + "x"
	wantOneProblem(t, "deep", blobNameProblems(id, filesAt(ok, deep)), "254")
	if got := blobNameProblems(id, filesAt(ok, "run1/"+strings.Repeat("d/", 251)+"x")); len(got) != 0 {
		t.Errorf("254 segments: %q", got)
	}

	longID := "20260101-" + strings.Repeat("a", 1010) + "-y-0000"
	if _, err := preparePublication(dataset(t, map[string]string{"x": "x"}), provenanceFlags{}, "", longID); err == nil || !strings.Contains(err.Error(), "1024") {
		t.Fatalf("publish: %v", err)
	}
}
