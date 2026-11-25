package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const datasetID = "20260101-x-y-0000"

// Generated dataset IDs differ between the two lbfs, so they are compared by shape.
var datasetGeneratedID = regexp.MustCompile(`[0-9]{8}-[a-z]+-[a-z]+-[0-9a-f]{4}`)

// The jsonschema library lists additional properties in Go's random map order.
var datasetAdditional = regexp.MustCompile(`additional properties .* not allowed`)

// jsonv2 picks "cannot" or "unable to" at random once per process.
var datasetModalVerb = regexp.MustCompile(`(cannot|unable to) unmarshal`)

func datasetDryRun(args ...string) []string {
	return append(append([]string{"publish"}, args...), "--dry-run", "--id", datasetID)
}

func runDatasetCases(t *testing.T, cases map[string]conformCase) {
	t.Helper()
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			conform(t, c)
		})
	}
}

// Creates a file in setup, skipping the test where the file system cannot hold the name.
func datasetCreate(t *testing.T, w *world, rel string) {
	t.Helper()
	path := filepath.Join(w.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Skipf("cannot create %q here: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Skipf("cannot create %q here: %v", rel, err)
	}
}

// Skips the test where the file system folds the names in rels to one, as macOS and Windows do.
func datasetDistinct(t *testing.T, w *world, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		datasetCreate(t, w, rel)
	}
	entries, err := os.ReadDir(filepath.Join(w.dir, "run1"))
	must(t, err)
	if len(entries) < len(rels) {
		t.Skipf("the file system at %s folds these names together", w.dir)
	}
}

func datasetSymlinks(t *testing.T, w *world, links map[string]string) {
	t.Helper()
	for link, target := range links {
		path := filepath.Join(w.dir, filepath.FromSlash(link))
		must(t, os.MkdirAll(filepath.Dir(path), 0o755))
		if err := os.Symlink(filepath.FromSlash(target), path); err != nil {
			t.Skipf("cannot create symlinks here: %v", err)
		}
	}
}

func TestConformanceDatasetLayout(t *testing.T) {
	nested := map[string]string{
		"run1/a.txt": "a", "run1/sub/b.txt": "bb", "run1/sub/deeper/c.csv": "c,c", "run1/a-b": "x", "run1/a/x": "y",
		"run1/empty.bin": "", "run1/ünï cødé ✓.txt": "u", "run1/a b#1%.csv": "p", "run1/UPPER.CSV": "U", "run1/x.parquet": "q",
		"run1/noext": "n", "run1/.hidden": "h", "run1/img.TIFF": "t", "run1/data.tar.gz": "g", "run1/文/日本.h5": "j",
	}
	cases := map[string]conformCase{
		"nested folders":                 {files: nested, args: datasetDryRun("run1")},
		"nested folders as json":         {files: nested, args: datasetDryRun("run1", "--json")},
		"single file":                    {files: map[string]string{"run1/a.txt": "a"}, args: datasetDryRun("run1/a.txt")},
		"single empty file":              {files: map[string]string{"empty.csv": ""}, args: datasetDryRun("empty.csv")},
		"trailing slash":                 {files: map[string]string{"run1/a.txt": "a"}, args: datasetDryRun("run1/")},
		"dot segments":                   {files: map[string]string{"run1/a.txt": "a"}, args: datasetDryRun("./run1/.")},
		"dot dot segments":               {files: map[string]string{"run1/a.txt": "a", "other/x": "x"}, args: datasetDryRun("other/../run1")},
		"current folder":                 {files: map[string]string{"a.txt": "a"}, args: datasetDryRun(".")},
		"absolute path":                  {files: map[string]string{"run1/a.txt": "a"}, args: datasetDryRun("{dir}/run1")},
		"excluded files":                 {files: map[string]string{"run1/a.txt": "x", "run1/sub/b.txt": "x", "run1/.DS_Store": "x", "run1/sub/._b.txt": "x"}, args: datasetDryRun("run1")},
		"folders named like excluded":    {files: map[string]string{"run1/._results/a.txt": "x", "run1/._b.txt": "x", "run1/sub/.DS_Store": "x", "run1/.DS_Store/c.txt": "x"}, args: datasetDryRun("run1")},
		"only excluded files":            {files: map[string]string{"run1/.DS_Store": "x", "run1/._a": "x"}, args: datasetDryRun("run1")},
		"empty folder":                   {setup: func(t *testing.T, w *world) { must(t, os.MkdirAll(filepath.Join(w.dir, "empty"), 0o755)) }, args: datasetDryRun("empty")},
		"missing input":                  {args: datasetDryRun("nope")},
		"missing parent":                 {args: datasetDryRun("nope/deeper")},
		"file used as folder":            {files: map[string]string{"data/a.txt": "a"}, args: datasetDryRun("data/a.txt/x")},
		"many files":                     {files: datasetManyFiles(40), args: datasetDryRun("run1")},
		"generated id":                   {files: map[string]string{"run1/a.txt": "a"}, args: []string{"publish", "run1", "--dry-run"}, normalize: []*regexp.Regexp{datasetGeneratedID}},
		"generated id as json":           {files: map[string]string{"run1/a.txt": "a"}, args: []string{"publish", "run1", "--dry-run", "--json"}, normalize: []*regexp.Regexp{datasetGeneratedID}},
		"bad id":                         {files: map[string]string{"run1/a.txt": "a"}, args: []string{"publish", "run1", "--dry-run", "--id", "bad"}},
		"id with a capital":              {files: map[string]string{"run1/a.txt": "a"}, args: []string{"publish", "run1", "--dry-run", "--id", "20260101-X-y-0000"}},
		"id with a newline":              {files: map[string]string{"run1/a.txt": "a"}, args: []string{"publish", "run1", "--dry-run", "--id", "20260101-x-y-0000\n"}},
		"other container":                {files: map[string]string{"run1/a.txt": "a"}, args: datasetDryRun("run1", "--container", "silver")},
		"upload alias":                   {files: map[string]string{"run1/a.txt": "a"}, args: []string{"upload", "run1", "--dry-run", "--id", datasetID}},
		"bad dataset without dry run":    {files: map[string]string{"run1/nul.txt": "x"}, args: []string{"publish", "run1"}},
		"bad provenance without dry run": {files: map[string]string{"run1/a.txt": "x"}, args: []string{"publish", "run1", "--property", "plate"}},
	}
	runDatasetCases(t, cases)
}

func datasetManyFiles(n int) map[string]string {
	files := map[string]string{}
	for i := range n {
		files[filepath.ToSlash(filepath.Join("run1", "d"+string(rune('a'+i%5)), "f"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+".txt"))] = strings.Repeat("y", i)
	}
	return files
}

func TestConformanceDatasetNames(t *testing.T) {
	cases := map[string]conformCase{
		"crate name":                   {files: map[string]string{crateName: "{}"}, args: datasetDryRun(crateName)},
		"crate name inside the folder": {files: map[string]string{"inside/RO-CRATE-METADATA.JSON": "x", "inside/ro-crate-metadata.json": "y"}, args: datasetDryRun("inside")},
		"crate name as a folder": {setup: func(t *testing.T, w *world) { datasetCreate(t, w, "Ro-Crate-Metadata.json/a.txt") },
			args: datasetDryRun("Ro-Crate-Metadata.json")},
		"255-byte segment": {files: map[string]string{"run1/" + strings.Repeat("文", 85): "x"}, args: datasetDryRun("run1")},
		"blob name too long": {files: map[string]string{"x": "x"},
			args: []string{"publish", "x", "--dry-run", "--id", "20260101-" + strings.Repeat("a", 1010) + "-y-0000"}},
		"blob name fits": {files: map[string]string{"x": "x"},
			args: []string{"publish", "x", "--dry-run", "--id", "20260101-" + strings.Repeat("a", 1000) + "-y-0000"}},
		"nested too deeply": {files: map[string]string{"run1/" + strings.Repeat("d/", 252) + "x": "x"}, args: datasetDryRun("run1")},
		"nested deeply":     {files: map[string]string{"run1/" + strings.Repeat("d/", 251) + "x": "x"}, args: datasetDryRun("run1")},
		"reserved and allowed names": {files: map[string]string{
			"run1/CON": "x", "run1/con.txt": "x", "run1/nul .txt": "x", "run1/NUL  .tar.gz": "x", "run1/aux": "x", "run1/PRN.csv": "x", "run1/COM1": "x",
			"run1/lpt9.log": "x", "run1/CONIN$": "x", "run1/conout$.txt": "x", "run1/COM¹": "x", "run1/com².txt": "x", "run1/LPT³.dat": "x",
			"run1/console.txt": "x", "run1/nully": "x", "run1/COM10": "x", "run1/com0": "x", "run1/LPT": "x", "run1/conin": "x", "run1/my nul.txt": "x", "run1/aux_data": "x",
		}, args: datasetDryRun("run1")},
		"reserved folder": {files: map[string]string{"run1/sub/Aux/a.txt": "x", "run1/ok.txt": "x"}, args: datasetDryRun("run1")},
	}
	// Linux allows 255 bytes per name, so only macOS and Windows can hold a 600-byte one.
	if runtime.GOOS != "linux" {
		cases["long segment"] = conformCase{files: map[string]string{"run1/" + strings.Repeat("文", 200) + ".csv": "x"}, args: datasetDryRun("run1")}
	}
	if runtime.GOOS != "windows" {
		for name, rel := range map[string]string{
			"colon": "run1/12:30.csv", "trailing dot": "run1/trailing.", "trailing space": "run1/trail ", "backslash": `run1/back\slash.txt`,
			"star": "run1/a*b", "quote": `run1/q"u`, "angle": "run1/lt<", "pipe": "run1/a|b", "question": "run1/why?", "tab": "run1/tab\tx",
			"control": "run1/bell\x07", "C1 control": "run1/c1\u0085x", "dotted folder": "run1/sub./x",
		} {
			cases["refused "+name] = conformCase{setup: func(t *testing.T, w *world) { datasetCreate(t, w, "run1/ok.txt"); datasetCreate(t, w, rel) }, args: datasetDryRun("run1")}
		}
		files := map[string]string{}
		for i := range 15 {
			files["run1/c:"+string(rune('a'+i))] = "x"
		}
		cases["more than ten problems"] = conformCase{files: files, args: datasetDryRun("run1")}
	}
	for name, pair := range map[string][2]string{
		"case":                 {"run1/Same.txt", "run1/same.txt"},
		"unicode forms":        {"run1/caf\u00e9.txt", "run1/cafe\u0301.txt"},
		"final sigma":          {"run1/σ.txt", "run1/ς.txt"},
		"sharp s":              {"run1/ß.txt", "run1/ss.txt"},
		"dotless i":            {"run1/ı.txt", "run1/I.txt"},
		"file and folder":      {"run1/Data", "run1/data/x.txt"},
		"folders":              {"run1/A/x.txt", "run1/a/y.txt"},
		"folder of a folder":   {"run1/X/deep/a.txt", "run1/x/deep/b.txt"},
		"file inside a folder": {"run1/A/x.txt", "run1/a/x.txt"},
	} {
		cases["same name "+name] = conformCase{setup: func(t *testing.T, w *world) { datasetDistinct(t, w, pair[0], pair[1]) }, args: datasetDryRun("run1")}
	}
	cases["same name reported once"] = conformCase{setup: func(t *testing.T, w *world) {
		datasetDistinct(t, w, "run1/A/x.txt", "run1/a/y.txt")
		datasetCreate(t, w, "run1/a/z.txt")
	}, args: datasetDryRun("run1")}
	runDatasetCases(t, cases)
}

func TestConformanceDatasetLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows; junctions are tested instead")
	}
	cases := map[string]conformCase{
		"links followed": {files: map[string]string{"elsewhere/r.txt": "r", "elsewhere/dir/d.txt": "d", "run1/own.txt": "o"},
			setup: func(t *testing.T, w *world) {
				datasetSymlinks(t, w, map[string]string{"run1/file-link": "../elsewhere/r.txt", "run1/dir-link": "../elsewhere/dir"})
			}, args: datasetDryRun("run1"), ignoreFiles: []string{"run1/dir-link"}},
		"broken link and loop": {files: map[string]string{"elsewhere/dir/d.txt": "d", "run1/own.txt": "o"},
			setup: func(t *testing.T, w *world) {
				datasetSymlinks(t, w, map[string]string{"run1/dangling": "../gone", "run1/dir-link": "../elsewhere/dir", "elsewhere/dir/loop": "../../run1"})
			}, args: datasetDryRun("run1"), ignoreFiles: []string{"run1/dangling", "run1/dir-link", "elsewhere/dir/loop"}},
		"loop to itself": {files: map[string]string{"run1/a.txt": "a"},
			setup: func(t *testing.T, w *world) { datasetSymlinks(t, w, map[string]string{"run1/sub/up": ".."}) }, args: datasetDryRun("run1"), ignoreFiles: []string{"run1/sub/up"}},
		"excluded names that are broken links": {files: map[string]string{"run1/z.txt": "z"},
			setup: func(t *testing.T, w *world) {
				datasetSymlinks(t, w, map[string]string{"run1/._dangling": "../gone", "run1/._linked": "z.txt", "run1/.DS_Store": "z.txt"})
			}, args: datasetDryRun("run1"), ignoreFiles: []string{"run1/._dangling"}},
		"file link as input": {files: map[string]string{"annex/SHA256E-s4--abcdef.csv": "data"},
			setup: func(t *testing.T, w *world) {
				datasetSymlinks(t, w, map[string]string{"results.csv": "annex/SHA256E-s4--abcdef.csv"})
			},
			args: datasetDryRun("results.csv")},
		"folder link as input": {files: map[string]string{"store/job-9f3a/x.txt": "x"},
			setup: func(t *testing.T, w *world) { datasetSymlinks(t, w, map[string]string{"results": "store/job-9f3a"}) },
			args:  datasetDryRun("results"), ignoreFiles: []string{"results"}},
		"input through a linked folder": {files: map[string]string{"store/job-9f3a/x.txt": "x"},
			setup: func(t *testing.T, w *world) { datasetSymlinks(t, w, map[string]string{"via": "store"}) },
			args:  datasetDryRun("via/job-9f3a"), ignoreFiles: []string{"via"}},
		"broken link as input": {setup: func(t *testing.T, w *world) { datasetSymlinks(t, w, map[string]string{"gone-link": "gone"}) }, args: datasetDryRun("gone-link"), ignoreFiles: []string{"gone-link"}},
		"device as input":      {args: datasetDryRun("/dev/null")},
		"pipe": {files: map[string]string{"run1/a.txt": "a"}, setup: func(t *testing.T, w *world) {
			if _, err := exec.LookPath("mkfifo"); err != nil {
				t.Skip("no mkfifo")
			}
			must(t, exec.Command("mkfifo", filepath.Join(w.dir, "run1", "pipe")).Run())
		}, args: datasetDryRun("run1"), ignoreFiles: []string{"run1/pipe"}},
		"pipe as input": {setup: func(t *testing.T, w *world) {
			if _, err := exec.LookPath("mkfifo"); err != nil {
				t.Skip("no mkfifo")
			}
			must(t, exec.Command("mkfifo", filepath.Join(w.dir, "lone")).Run())
		}, args: datasetDryRun("lone"), ignoreFiles: []string{"lone"}},
	}
	if os.Geteuid() != 0 {
		cases["unreadable file and folder"] = conformCase{files: map[string]string{"run1/a.txt": "a", "run1/b.txt": "b", "run1/locked/c.txt": "c"},
			setup: func(t *testing.T, w *world) {
				must(t, os.Chmod(filepath.Join(w.dir, "run1", "a.txt"), 0))
				must(t, os.Chmod(filepath.Join(w.dir, "run1", "locked"), 0))
				t.Cleanup(func() {
					os.Chmod(filepath.Join(w.dir, "run1", "locked"), 0o755)
					os.Chmod(filepath.Join(w.dir, "run1", "a.txt"), 0o644)
				})
			}, args: datasetDryRun("run1"), ignoreFiles: []string{"run1/locked", "run1/a.txt"}}
	}
	runDatasetCases(t, cases)
}

func TestConformanceDatasetJunctions(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows only")
	}
	junction := func(link, target string) func(t *testing.T, w *world) {
		return func(t *testing.T, w *world) {
			if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(w.dir, link), filepath.Join(w.dir, target)).CombinedOutput(); err != nil {
				t.Skipf("cannot create a junction: %v %s", err, out)
			}
		}
	}
	runDatasetCases(t, map[string]conformCase{
		"junction loop":     {files: map[string]string{"run1/a.txt": "a"}, setup: junction(`run1\loop`, "run1"), args: datasetDryRun("run1"), ignoreFiles: []string{"run1/loop"}},
		"junction followed": {files: map[string]string{"run1/a.txt": "a", "elsewhere/b.txt": "b"}, setup: junction(`run1\other`, "elsewhere"), args: datasetDryRun("run1"), ignoreFiles: []string{"run1/other"}},
		"junction as input": {files: map[string]string{"elsewhere/b.txt": "b"}, setup: junction("link", "elsewhere"), args: datasetDryRun("link"), ignoreFiles: []string{"link"}},
	})
}

const datasetTool = `[{"name": "a", "version": "1", "url": "https://a"}]`

func TestConformanceDatasetProvenance(t *testing.T) {
	data := map[string]string{"data/a.txt": "a"}
	with := func(extra map[string]string) map[string]string {
		files := map[string]string{"data/a.txt": "a"}
		for k, v := range extra {
			files[k] = v
		}
		return files
	}
	cases := map[string]conformCase{}
	for name, body := range map[string]string{
		"accepted":                 `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "CellProfiler", "version": "4.2.6", "url": "https://cellprofiler.org"}]}`,
		"everything":               `{"name": "N", "description": "D", "derived_from": "20260101-x-y-0000", "instruments": ` + datasetTool + `, "properties": {"sample": "S", "k\u00e9y": "v"}}`,
		"repeated instrument":      `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "https://a"}, {"name": "a", "version": "1", "url": "https://a"}]}`,
		"two instruments":          `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "https://a"}, {"name": "b", "version": "2", "url": "https://b"}]}`,
		"opaque url":               `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "mailto:x"}]}`,
		"nulls":                    `{"name": null, "instruments": null, "derived_from": null, "properties": null}`,
		"null":                     `null`,
		"byte-order mark":          "\uFEFF" + `{"derived_from": "20260101-x-y-0000", "instruments": ` + datasetTool + `}`,
		"unicode":                  `{"name": "caf\u00e9 \ud83d\ude00", "description": "a\u0000b"}`,
		"whitespace":               "\t {\"name\": \"x\"} \r\n",
		"trailing brace":           `{"properties": {"sample_id": "S1"}}}`,
		"trailing bracket":         `{"properties": {}}]`,
		"trailing content":         `{"properties": {}} {"derived_from": "x"}`,
		"trailing garbage":         `{"name":"x"}garbage`,
		"mis-cased field":          `{"Derived_From": "20260101-x-y-0000", "instruments": ` + datasetTool + `}`,
		"upper-case field":         `{"derived_from": "20260101-x-y-0000", "INSTRUMENTS": ` + datasetTool + `}`,
		"mis-cased tool field":     `{"derived_from": "20260101-x-y-0000", "instruments": [{"Name": "a", "version": "1", "url": "https://a"}]}`,
		"unknown field":            `{"properties": {}, "instrumentz": []}`,
		"unknown nested field":     `{"instruments": [{"zzz": 1}]}`,
		"unknown escaped name":     `{"zz~/x": 1}`,
		"duplicate field":          `{"properties": {}, "properties": {"a": "b"}}`,
		"duplicate property":       `{"properties": {"a": "b", "a": "c"}}`,
		"duplicate tool field":     `{"instruments": [{"name": "a", "name": "b"}]}`,
		"duplicate in a value":     `{"name": {"a":1,"a":2}}`,
		"non-string property":      `{"properties": {"n": 1}}`,
		"boolean property":         `{"properties": {"a": true}}`,
		"array property":           `{"properties": {"a": [1]}}`,
		"object property":          `{"properties": {"a": {"b": 1}}}`,
		"number name":              `{"name": 5}`,
		"huge number name":         `{"name": 1e400}`,
		"leading zero":             `{"name": 01}`,
		"object instruments":       `{"instruments": {}}`,
		"number instrument":        `{"instruments": [5]}`,
		"number version":           `{"instruments": [{"version": 1}]}`,
		"array provenance":         `[]`,
		"string provenance":        `"x"`,
		"number provenance":        `5`,
		"empty":                    ``,
		"only whitespace":          ` `,
		"open brace":               `{`,
		"trailing comma":           `{"name": "a",}`,
		"trailing comma in array":  `{"instruments": [{"name": "a", "version": "1", "url": "https://a"}, ]}`,
		"missing comma":            `{"name": "a" "b"}`,
		"missing comma in array":   `{"instruments": [{"name": "a"} {"name": "b"}]}`,
		"missing colon":            `{"name" "a"}`,
		"single quotes":            `{"name": 'a'}`,
		"bad escape":               `{"name": "a\x"}`,
		"lone surrogate":           `{"name": "\ud800"}`,
		"bad surrogate pair":       `{"name": "a\ud800A"}`,
		"low surrogate first":      `{"name": "a\uDC00"}`,
		"cut escape":               `{"name": "a\u12`,
		"unterminated string":      `{"name": "abc`,
		"bad literal":              `{"name": tru}`,
		"cut literal":              `{"name": tr`,
		"bad number":               `{"name": -}`,
		"bad fraction":             `{"name": -1.}`,
		"bad exponent":             `{"name": 1e}`,
		"number name key":          `{5: "a"}`,
		"close bracket value":      `{"name": ]`,
		"close brace in array":     `{"instruments": [}`,
		"array never closed":       `{"instruments": [1}`,
		"object closed by bracket": `{"instruments": {"a": 1]}`,
		"value of an array cut":    `{"name": [1,}`,
		"cut after name":           `{"name"`,
		"cut after colon":          `{"name":`,
		"control character":        "{\"name\": \"a\tb\"}",
		"invalid UTF-8":            "{\"name\": \"caf\xe9\"}",
		"parent without tools":     `{"derived_from": "20260101-x-y-0000"}`,
		"tools without parent":     `{"instruments": [{"name": "a", "version": "1", "url": "u"}]}`,
		"incomplete instrument":    `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a"}]}`,
		"bad parent id":            `{"derived_from": "../x", "instruments": ` + datasetTool + `, "properties": {}}`,
		"url not a uri":            `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "not a url"}]}`,
		"url with a space":         `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "https://a b"}]}`,
		"url with a bad escape":    `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "https://a/%zz"}]}`,
		"url with a bad port":      `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "https://a:80x"}]}`,
		"url without scheme":       `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": ":y"}]}`,
		"absolute path url":        `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "/abs/path"}]}`,
		"tools sharing a url":      `{"derived_from": "20260101-x-y-0000", "instruments": [{"name": "a", "version": "1", "url": "https://a"}, {"name": "b", "version": "1", "url": "https://a"}]}`,
		"reserved property":        `{"properties": {"source_path": "/elsewhere"}}`,
		"two reserved properties":  `{"properties": {"subscription_id": "x", "source_path": "y"}}`,
		"property named run":       `{"properties": {"run": "7"}}`,
		"property uploader":        `{"properties": {"uploader": "x"}}`,
		"property blob-location":   `{"properties": {"blob-location": "x"}}`,
		"property source-":         `{"properties": {"source-20260101-x-y-0000": "x"}}`,
		"property @id":             `{"properties": {"@id": "x"}}`,
	} {
		cases["provenance file "+name] = conformCase{files: with(map[string]string{"p.json": body}), args: datasetDryRun("data", "--provenance", "p.json"), normalize: []*regexp.Regexp{datasetModalVerb}}
	}
	for name, body := range map[string]string{
		"accepted":         `{"sample_id": "SAM-0001"}`,
		"mixed case":       `{"Sample_ID": "S1", "sample_id": "S2"}`,
		"byte-order mark":  "\uFEFF" + `{"sample_id": "S1"}`,
		"non-string":       `{"n": 1, "f": 0.5, "e": 1e3, "b": true, "z": false}`,
		"list value":       `{"n": [1]}`,
		"object value":     `{"n": {"a": 1}}`,
		"null value":       `{"n": null}`,
		"null":             `null`,
		"trailing bracket": `{"sample_id": "S1"}]`,
		"trailing brace":   `{"sample_id": "S1"}}`,
		"array":            `[]`,
		"duplicate":        `{"a":"b","a":"c"}`,
		"empty":            ``,
		"trailing comma":   `{"a": "b",}`,
		"reserved":         `{"source_path": "x"}`,
	} {
		cases["properties file "+name] = conformCase{files: with(map[string]string{"props.json": body}), args: datasetDryRun("data", "--properties", "props.json"), normalize: []*regexp.Regexp{datasetModalVerb}}
	}
	common := with(map[string]string{"common.json": `{"name": "Plate P-0001", "properties": {"operator": "A. Researcher"}}`, "props.json": `{"sample_id": "S1"}`})
	tool := "name=nf,version=0.1.0,url=https://example.org/a,b"
	for name, args := range map[string][]string{
		"missing provenance file":       {"--provenance", "nope.json"},
		"missing properties file":       {"--properties", "nope.json"},
		"properties and property":       {"--properties", "props.json", "--property", "sample_id=x"},
		"properties and new property":   {"--properties", "props.json", "--property", "plate=P"},
		"property given twice":          {"--property", "a=1", "--property", "a=2"},
		"property without value":        {"--property", "plate"},
		"property without name":         {"--property", "=x"},
		"property with equals":          {"--property", "note=a=b"},
		"property set by lbf":           {"--property", "source_path=/x"},
		"property @id":                  {"--property", "@id=x"},
		"property @none":                {"--property", "@none=x"},
		"property names encoded":        {"--property", "plate id=P 1", "--property", "a/b=c", "--property", "ü=v"},
		"file and property twice":       {"--provenance", "common.json", "--property", "operator=B"},
		"file and name":                 {"--provenance", "common.json", "--name", "Other"},
		"file with flags":               {"--provenance", "common.json", "--description", "D", "--property", "plate_id=P-0001", "--property", "note=a=b"},
		"file and derived-from":         {"--provenance", "common.json", "--derived-from", datasetID},
		"file and instrument":           {"--provenance", "common.json", "--instrument", tool},
		"file and properties":           {"--provenance", "common.json", "--properties", "props.json"},
		"derived-from alone":            {"--derived-from", datasetID},
		"instrument alone":              {"--instrument", tool},
		"derived with a comma in a url": {"--derived-from", datasetID, "--instrument", tool},
		"derived from a name":           {"--derived-from", "my-dataset", "--instrument", tool},
		"derived from a capital":        {"--derived-from", "20260101-X-y-0000", "--instrument", tool},
		"derived from a short id":       {"--derived-from", "20260101-x-y-000", "--instrument", tool},
		"derived from a versioned id":   {"--derived-from", "20260101-x-y-0000.v2", "--instrument", tool},
		"malformed instrument":          {"--derived-from", datasetID, "--instrument", "nf"},
		"unknown instrument key":        {"--derived-from", datasetID, "--instrument", "colour=red,name=nf"},
		"incomplete instrument":         {"--derived-from", datasetID, "--instrument", "name=nf"},
		"instrument keys reordered":     {"--derived-from", datasetID, "--instrument", "url=https://a,version=1,name=x,y"},
		"instrument key cased":          {"--derived-from", datasetID, "--instrument", "Name=x,version=1,url=https://a"},
		"tools sharing a url":           {"--derived-from", datasetID, "--instrument", "name=align,version=1.0,url=https://example.org/pipe", "--instrument", "name=qc,version=2.0,url=https://example.org/pipe"},
		"same tool twice":               {"--derived-from", datasetID, "--instrument", "name=align,version=1.0,url=https://example.org/pipe", "--instrument", "name=align,version=1.0,url=https://example.org/pipe"},
		"everything":                    {"--derived-from", datasetID, "--instrument", "name=a,version=1,url=https://a", "--instrument", "name=b,version=2,url=https://b", "--name", "Run", "--description", "Desc", "--property", "x=1"},
		"name and description":          {"--name", "N <&> \u2028", "--description", "D"},
	} {
		cases["flags "+name] = conformCase{files: common, args: datasetDryRun(append([]string{"data"}, args...)...), normalize: []*regexp.Regexp{datasetModalVerb}}
	}
	cases["flags json"] = conformCase{files: data, args: datasetDryRun("data", "--json", "--name", "N")}
	runDatasetCases(t, cases)
}

func TestConformanceDatasetProfiles(t *testing.T) {
	cellRun := map[string]string{"run1/plate_map.csv": "m", "run1/features/P001.parquet": "f"}
	good := []string{"--derived-from", datasetID, "--instrument", "name=CellProfiler,version=4.2.6,url=https://cellprofiler.org", "--property", "sample_id=S1", "--property", "plate_id=P001"}
	with := func(files map[string]string, extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range files {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	flat := with(cellRun, map[string]string{"profiles/minimal-silver/profile.json": minimalSilverProfile, "profiles/cell-painting/profile.json": cellPaintingProfile})
	versioned := with(cellRun, map[string]string{"profiles/minimal-silver/0.1.0/profile.json": minimalSilverProfile, "profiles/cell-painting/0.1.0/profile.json": cellPaintingProfile})
	cases := map[string]conformCase{
		"cell painting":               {files: flat, args: datasetDryRun(append([]string{"run1", "--profile", "profiles/cell-painting"}, good...)...)},
		"cell painting versioned":     {files: versioned, args: datasetDryRun(append([]string{"run1", "--profile", "profiles/cell-painting/0.1.0"}, good...)...)},
		"profile file named directly": {files: versioned, args: datasetDryRun(append([]string{"run1", "--profile", "profiles/cell-painting/0.1.0/profile.json"}, good...)...)},
		"minimal silver":              {files: flat, args: datasetDryRun(append([]string{"run1", "--profile", "profiles/minimal-silver"}, good...)...)},
		"stray file":                  {files: with(flat, map[string]string{"run1/notes.txt": "n"}), args: datasetDryRun(append([]string{"run1", "--profile", "profiles/cell-painting"}, good...)...)},
		"no plate map":                {files: with(flat, nil), args: datasetDryRun(append([]string{"run1/features", "--profile", "profiles/cell-painting"}, good...)...)},
		"no parent":                   {files: flat, args: datasetDryRun("run1", "--profile", "profiles/cell-painting", "--property", "sample_id=S1")},
		"wrong instrument":            {files: flat, args: datasetDryRun("run1", "--profile", "profiles/cell-painting", "--derived-from", datasetID, "--instrument", "name=Fiji,version=2,url=https://fiji.sc")},
		"missing profile":             {files: flat, args: datasetDryRun("run1", "--profile", "profiles/nope")},
		"profiles folder itself":      {files: flat, args: datasetDryRun("run1", "--profile", "profiles")},
		"loose profile":               {files: with(cellRun, map[string]string{"p/loose/profile.json": `{"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "https://example.org/profiles/loose/0.1.0"}`}), args: datasetDryRun("run1", "--profile", "p/loose")},
		"untitled versioned profile":  {files: with(cellRun, map[string]string{"profiles/untitled/1.2/profile.json": `{"$id": "https://example.org/profiles/untitled/1.2", "$ref": "https://github.com/ImperialCollegeLondon/lbf-cli/tree/main/profiles/bronze/0.7.0"}`}), args: datasetDryRun(append([]string{"run1", "--profile", "profiles/untitled/1.2"}, good[:4]...)...)},
		"bronze id reused":            {files: with(cellRun, map[string]string{"p/fake/profile.json": `{"$id": "https://github.com/ImperialCollegeLondon/lbf-cli/tree/main/profiles/bronze/0.7.0"}`}), args: datasetDryRun("run1", "--profile", "p/fake")},
		"siblings only from profiles": {files: with(cellRun, map[string]string{
			"proj/cfg/profile.json": `{"$id": "./"}`, "proj/1.0/profile.json": `{"$id": "./"}`, "profiles/notes/profile.json": `{"$id": "notes"}`,
			"profiles/zz/cfg/profile.json":               `{"$id": "https://example.org/profiles/minimal-silver/0.1.0", "type": "nonsense"}`,
			"profiles/minimal-silver/0.1.0/profile.json": minimalSilverProfile, "profiles/cell-painting/profile.json": cellPaintingProfile,
			"profiles/broken/0.1.0/profile.json": `{"$id": "https://example.org/profiles/broken/0.1.0", "type": 5}`,
		}), args: datasetDryRun(append([]string{"run1", "--profile", "profiles/cell-painting"}, good...)...)},
		"broken versioned profile": {files: with(cellRun, map[string]string{"profiles/broken/0.1.0/profile.json": `{"$id": "https://example.org/profiles/broken/0.1.0", "type": 5}`}), args: datasetDryRun("run1", "--profile", "profiles/broken/0.1.0")},
		"unrelated profile.json nearby": {files: with(cellRun, map[string]string{"app/cfg/profile.json": `{"theme": "dark"}`, "work/notes/profile.json": `not json`, "work/myprof/profile.json": minimalSilverProfile}),
			args: datasetDryRun(append([]string{"run1", "--profile", "work/myprof"}, good...)...)},
		"profile without id": {files: with(cellRun, map[string]string{"work/bad/profile.json": `{"theme": "dark"}`}), args: datasetDryRun("run1", "--profile", "work/bad")},
	}
	for _, id := range []string{"./", "run1/a.txt", crateName, "#uploader", "profiles/x", "https://example.org/p#part", "urn:example:p", "file:///p"} {
		cases["profile id "+id] = conformCase{files: with(cellRun, map[string]string{"p/x/profile.json": `{"$id": "` + id + `"}`}), args: datasetDryRun("run1", "--profile", "p/x")}
	}
	bronze := `"https://github.com/ImperialCollegeLondon/lbf-cli/tree/main/profiles/bronze/0.7.0"`
	for name, body := range map[string]string{
		"type not a type":                    `"type": 5`,
		"two keyword errors":                 `"$anchor": "1bad", "minLength": -1`,
		"required not an array":              `"required": "x"`,
		"negative minLength":                 `"minLength": -1`,
		"bad pattern":                        `"properties": {"name": {"pattern": "(["}}`,
		"lookahead":                          `"properties": {"name": {"pattern": "(?=x)"}}`,
		"unexpected paren":                   `"properties": {"name": {"pattern": "a)"}}`,
		"missing paren":                      `"properties": {"name": {"pattern": "(a"}}`,
		"repeat nothing":                     `"properties": {"name": {"pattern": "*a"}}`,
		"repeat a repeat":                    `"properties": {"name": {"pattern": "a**"}}`,
		"backreference":                      `"properties": {"name": {"pattern": "\\1"}}`,
		"digits":                             `"properties": {"name": {"pattern": "\\d{3}$"}}`,
		"unknown keywords":                   `"frobnicate": 1, "x-lbf-unit": "nm"`,
		"id in capitals":                     `"$ID": "https://example.org/up"`,
		"not json":                           `x`,
		"trailing content":                   `"title": "t"} x`,
		"id a number":                        `"$id": 5`,
		"ref to nowhere":                     `"$ref": "https://example.org/missing"`,
		"many failures":                      `"properties": {"name": {"const": "zzz", "maxLength": 2}, "hasPart": {"maxItems": 0, "items": {"properties": {"contentSize": {"maxLength": 0}, "@id": {"enum": ["a", 1, null]}}}}, "additionalProperty": {"minProperties": 9}}, "required": ["nope", "nada"], "additionalProperties": false`,
		"email format":                       `"properties": {"identifier": {"format": "email"}}`,
		"date-time format":                   `"properties": {"name": {"format": "date-time"}}`,
		"date format":                        `"properties": {"name": {"format": "date"}}`,
		"uuid format":                        `"properties": {"name": {"format": "uuid"}}`,
		"hostname format":                    `"properties": {"name": {"format": "hostname"}}`,
		"ipv4 format":                        `"properties": {"name": {"format": "ipv4"}}`,
		"uri format":                         `"properties": {"name": {"format": "uri"}}`,
		"unknown format":                     `"properties": {"name": {"format": "no-such-format"}}`,
		"anyOf":                              `"properties": {"name": {"anyOf": [{"const": "a"}, {"maxLength": 2}, {"type": "number"}]}}`,
		"oneOf none":                         `"properties": {"name": {"oneOf": [{"const": "a"}, {"maxLength": 2}]}}`,
		"oneOf two":                          `"properties": {"name": {"oneOf": [{"minLength": 1}, {"maxLength": 20}]}}`,
		"not":                                `"properties": {"name": {"not": {"type": "string"}}}`,
		"if then else":                       `"if": {"properties": {"name": {"const": "run1"}}}, "then": {"required": ["zz"]}, "else": {"required": ["yy"]}`,
		"propertyNames":                      `"propertyNames": {"maxLength": 12}`,
		"patternProperties":                  `"patternProperties": {"^d": {"type": "number"}}`,
		"minContains":                        `"properties": {"conformsTo": {"uniqueItems": true, "minContains": 2, "contains": {"type": "string"}}}`,
		"maxContains":                        `"properties": {"conformsTo": {"maxContains": 0, "contains": {"type": "string"}}}`,
		"minContains none":                   `"properties": {"conformsTo": {"minContains": 3, "contains": {"type": "number"}}}`,
		"enum of objects":                    `"properties": {"name": {"enum": [{"a": 1}, [1]]}}`,
		"const object":                       `"properties": {"name": {"const": {"a": 1}}}`,
		"numbers printed":                    `"$defs": {"properties": {"properties": {"temperature": {"type": "number", "minimum": 1000000, "exclusiveMaximum": 1, "multipleOf": 0.25}}}}`,
		"numbers grouped":                    `"$defs": {"properties": {"properties": {"temperature": {"type": "number", "minimum": 2500, "maximum": 0.0001}}}}`,
		"defs":                               `"$defs": {"n": {"type": "integer"}}, "properties": {"name": {"$ref": "#/$defs/n"}}`,
		"anchor":                             `"$defs": {"n": {"$anchor": "num", "type": "integer"}}, "properties": {"name": {"$ref": "#num"}}`,
		"dependentRequired":                  `"dependentRequired": {"identifier": ["zz", "yy"]}`,
		"dependentSchemas":                   `"dependentSchemas": {"identifier": {"required": ["qq"]}}`,
		"prefixItems":                        `"properties": {"hasPart": {"prefixItems": [{"required": ["zz"]}], "items": false}}`,
		"additionalProperties":               `"additionalProperties": {"type": "number"}`,
		"min and max length":                 `"properties": {"name": {"maxLength": 2, "minLength": 10}}`,
		"false schema":                       `"properties": {"name": false}`,
		"several types":                      `"properties": {"datePublished": {"type": ["null", "number", "integer", "array"]}}`,
		"enum printed":                       `"properties": {"identifier": {"enum": ["x", 1.50, null, true, 1e3]}}`,
		"const number":                       `"properties": {"identifier": {"const": 1.50}}`,
		"type misspelt":                      `"type": "objekt"`,
		"allOf with booleans":                `"properties": {"name": {"allOf": [{"maxLength": 1}, {"pattern": "^x"}, true, false]}}`,
		"wrong keyword types":                `"properties": 5, "minItems": 1.5`,
		"empty allOf":                        `"allOf": []`,
		"bad anchor":                         `"$anchor": "1bad"`,
		"ref to another profile":             `"$ref": "https://example.org/profiles/minimal-silver/0.1.0"`,
		"required twice":                     `"required": ["creator", "creator"]`,
		"repeated enum":                      `"enum": [1, 1]`,
		"builds on bronze":                   `"$ref": ` + bronze + `, "required": ["isBasedOn"]`,
		"unevaluated at the root":            `"unevaluatedProperties": false`,
		"unevaluated after properties":       `"properties": {"name": true}, "unevaluatedProperties": false`,
		"unevaluated after bronze":           `"$ref": ` + bronze + `, "unevaluatedProperties": false`,
		"unevaluated through a ref":          `"allOf": [{"$ref": "#/$defs/c"}], "unevaluatedProperties": false, "$defs": {"c": {"properties": {"identifier": true, "creator": true}}}`,
		"unevaluated after allOf":            `"allOf": [{"properties": {"identifier": true}}, {"properties": {"conformsTo": true}}], "unevaluatedProperties": {"type": "number"}`,
		"unevaluated after anyOf":            `"anyOf": [{"properties": {"identifier": true}}, {"properties": {"datePublished": true}}], "unevaluatedProperties": false`,
		"unevaluated items":                  `"properties": {"conformsTo": {"unevaluatedItems": false}}`,
		"unevaluated after contains":         `"properties": {"conformsTo": {"contains": {"const": "x"}, "unevaluatedItems": false}}`,
		"unevaluated after prefixItems":      `"properties": {"conformsTo": {"prefixItems": [true], "unevaluatedItems": {"type": "number"}}}`,
		"unevaluated after if":               `"if": {"properties": {"identifier": {"type": "string"}}}, "then": {"properties": {"creator": true}}, "unevaluatedProperties": false`,
		"unevaluated after patterns":         `"properties": {"additionalProperty": {"patternProperties": {"^n": true}, "unevaluatedProperties": false}}`,
		"unevaluated after oneOf":            `"oneOf": [{"properties": {"identifier": true}}, {"required": ["zz"]}], "unevaluatedProperties": false`,
		"unevaluated after dependentSchemas": `"dependentSchemas": {"identifier": {"properties": {"creator": true}}}, "unevaluatedProperties": false`,
		"ref to the folder":                  `"$ref": "#"`,
		"ref to a missing file":              `"$ref": "profiles/other"`,
		"ref to a file":                      `"$ref": "profiles/plain.json"`,
		"dependencies list":                  `"dependencies": {"identifier": ["zz", "yy"]}`,
		"dependencies schema":                `"dependencies": {"identifier": {"required": ["qq"]}}`,
		"dependencies not an object":         `"dependencies": 5`,
		"bad property pattern":               `"patternProperties": {"(": true}`,
		"property rules": `"$defs": {"properties": {"required": ["plate_id", "temperature"], "properties": {
			"plate_id": {"pattern": "^P-[0-9]{4}$"}, "temperature": {"enum": ["4C", "RT"]}, "notes": {"maxLength": 3}}}}`,
	} {
		cases["schema "+name] = conformCase{
			files: with(cellRun, map[string]string{"profiles/x/profile.json": `{"$id": "https://example.org/profiles/x/0.1.0", ` + body + `}`, "profiles/minimal-silver/profile.json": minimalSilverProfile,
				"profiles/plain.json": `{"type": "object", "required": ["zz"]}`}),
			args: datasetDryRun("run1", "--profile", "profiles/x", "--property", "plate_id=P1", "--property", "notes=long", "--property", "temperature=37"),
		}
	}
	plate := with(map[string]string{"plate1/readings.csv": "a,b"}, map[string]string{
		"profiles/plate-read/0.1.0/profile.json": plateReadProfile, "profiles/minimal-silver/0.1.0/profile.json": minimalSilverProfile,
		"other/plate-read/0.1.0/profile.json": strings.Replace(plateReadProfile, `"Plate read"`, `"Plate"`, 1),
		"else/plate-read/0.1.0/profile.json":  strings.Replace(plateReadProfile, "https://example.org/profiles/plate-read", "https://example.com/plate-read", 1),
		"old/silver/0.1.0/profile.json":       `{"$id": "https://example.org/profiles/silver/0.1.0", "$ref": "https://github.com/ImperialCollegeLondon/lbf-data-tools/tree/main/profiles/bronze/0.4.0"}`,
		"ro-crate-profiles/a.txt":             "a",
	})
	read := func(extra ...string) []string {
		return datasetDryRun(append([]string{"plate1", "--profile", "profiles/plate-read/0.1.0"}, extra...)...)
	}
	for name, args := range map[string][]string{
		"typed":                 read("--property", "plate_id=P-0001", "--property", "wavelength_nm=450", "--property", "lidded=true"),
		"typed json":            read("--property", "plate_id=P-0001", "--property", "wavelength_nm=450", "--json"),
		"pattern":               read("--property", "plate_id=X1"),
		"range":                 read("--property", "plate_id=P-0001", "--property", "wavelength_nm=9000"),
		"below range":           read("--property", "plate_id=P-0001", "--property", "wavelength_nm=-0.5"),
		"not a number":          read("--property", "plate_id=P-0001", "--property", "wavelength_nm=blue"),
		"not a whole":           read("--property", "plate_id=P-0001", "--property", "wavelength_nm=450.5"),
		"not true":              read("--property", "plate_id=P-0001", "--property", "lidded=yes"),
		"missing":               read(),
		"unasked":               read("--property", "plate_id=P-0001", "--property", "colour=red", "--property", "angle=3"),
		"no readings":           datasetDryRun("plate1/readings.csv", "--profile", "profiles/plate-read/0.1.0", "--property", "plate_id=P-0001"),
		"two profiles":          read("--profile", "profiles/minimal-silver/0.1.0", "--property", "plate_id=P-0001", "--property", "sample_id=S1", "--derived-from", datasetID, "--instrument", "name=nf,version=1,url=https://example.org/nf"),
		"same twice":            read("--profile", "profiles/plate-read/0.1.0", "--property", "plate_id=P-0001"),
		"copies differ":         read("--profile", "other/plate-read/0.1.0", "--property", "plate_id=P-0001"),
		"name clash":            read("--profile", "else/plate-read/0.1.0", "--property", "plate_id=P-0001"),
		"old bronze":            datasetDryRun("plate1", "--profile", "old/silver/0.1.0"),
		"schemas name":          datasetDryRun("ro-crate-profiles"),
		"empty profile":         datasetDryRun("plate1", "--profile", ""),
		"properties file typed": append(read("--properties", "typed.json"), "--property", "plate_id=P-0001"),
	} {
		files := with(plate, map[string]string{"typed.json": `{"wavelength_nm": 450, "lidded": false}`})
		cases["plate read "+name] = conformCase{files: files, args: args}
	}
	ready := with(nil, map[string]string{"profiles/plate-read/0.1.0/profile.json": plateReadProfile, "profiles/rough/0.1.0/profile.json": roughProfile,
		"profiles/broken/0.1.0/profile.json": `{"$id": "https://example.org/profiles/broken/0.1.0", "type": 5}`})
	for name, args := range map[string][]string{
		"ready":       {"profiles", "check", "profiles/plate-read/0.1.0"},
		"rough":       {"profiles", "check", "profiles/rough/0.1.0"},
		"broken":      {"profiles", "check", "profiles/broken/0.1.0"},
		"missing":     {"profiles", "check", "profiles/nope"},
		"no folder":   {"profiles", "check"},
		"no check":    {"profiles"},
		"other word":  {"profiles", "list"},
		"extra":       {"profiles", "check", "a", "b"},
		"help":        {"profiles", "--help"},
		"file itself": {"profiles", "check", "profiles/plate-read/0.1.0/profile.json"},
	} {
		cases["profiles check "+name] = conformCase{files: ready, args: args}
	}
	for name, c := range cases {
		c.normalize = append(c.normalize, datasetAdditional)
		cases[name] = c
	}
	runDatasetCases(t, cases)
}

func TestConformanceDatasetNewID(t *testing.T) {
	conform(t, conformCase{args: []string{"new-id"}, normalize: []*regexp.Regexp{datasetGeneratedID}})
	conform(t, conformCase{args: []string{"new-id", "extra"}})
}
