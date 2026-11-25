package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

// Characters PowerShell's culture comparisons ignore or fold, which Go compares byte for byte.
const (
	zwsp = "\u200b"
	zwj  = "\u200d"
	bom  = "\ufeff"
	shy  = "\u00ad"
	nfc  = "caf\u00e9"
	nfd  = "cafe\u0301"
)

// Names whose UTF-8 byte order differs from case-insensitive, culture and UTF-16 order.
var stringsByteOrder = map[string]string{
	"run1/B.txt": "b", "run1/a.txt": "a", "run1/\u00c9.txt": "e", "run1/z.txt": "z", "run1/\U0001F600.txt": "smile",
	"run1/\uff21.txt": "fullwidth", "run1/\ue000.txt": "private", "run1/a b.txt": "space", "run1/a-b": "dash", "run1/a.b": "dot",
	"run1/\u00e4/x.txt": "umlaut", "run1/_x": "underscore",
}

func TestConformanceStringsCLI(t *testing.T) {
	data := map[string]string{"data/a.txt": "a"}
	runDatasetCases(t, map[string]conformCase{
		"command in capitals":                {files: data, args: []string{"PUBLISH", "data", "--dry-run", "--id", datasetID}},
		"command with a zero-width space":    {files: data, args: []string{"publish" + zwsp, "data", "--dry-run", "--id", datasetID}},
		"alias with a soft hyphen":           {files: data, args: []string{"up" + shy + "load", "data", "--dry-run", "--id", datasetID}},
		"help with a zero-width space":       {args: []string{"--help" + zwsp}},
		"version with a soft hyphen":         {args: []string{"version" + shy}},
		"help flag with a zero-width space":  {args: []string{"fetch", "--help" + zwsp}},
		"flag in capitals":                   {files: data, args: datasetDryRun("data", "--Name", "x")},
		"flag with a zero-width joiner":      {files: data, args: datasetDryRun("data", "--name"+zwj+"=x")},
		"boolean with a zero-width space":    {files: data, args: datasetDryRun("data", "--json=true"+zwsp)},
		"boolean in odd case":                {files: data, args: datasetDryRun("data", "--json=tRUE")},
		"mode with a zero-width space":       {args: []string{"check", "--mode", "upload" + zwsp, "--sas-env", "x"}},
		"mode in capitals":                   {args: []string{"mint-sas", "--mode", "Download"}},
		"id of only a zero-width space":      {files: data, args: []string{"publish", "data", "--dry-run", "--id", zwsp}},
		"name of only a zero-width space":    {files: data, args: datasetDryRun("data", "--name", zwsp)},
		"name of only a byte-order mark":     {files: data, args: datasetDryRun("data", "--name", bom)},
		"description of only a soft hyphen":  {files: data, args: datasetDryRun("data", "--description", shy)},
		"out of only a zero-width space":     {args: []string{"fetch", "--out", zwsp, "--sas-env", "missing.env", "x"}},
		"sas-env of only a zero-width space": {args: []string{"check", "--mode", "download", "--sas-env", zwsp}},
	})
}

func TestConformanceStringsProvenance(t *testing.T) {
	with := func(extra map[string]string) map[string]string {
		files := map[string]string{"data/a.txt": "a"}
		for k, v := range extra {
			files[k] = v
		}
		return files
	}
	files := with(map[string]string{"props.json": `{"sample_id": "S1", "` + nfc + `": "v"}`})
	derived := func(tools ...string) []string {
		args := []string{"--derived-from", datasetID}
		for _, tool := range tools {
			args = append(args, "--instrument", tool)
		}
		return args
	}
	cases := map[string]conformCase{}
	for name, args := range map[string][]string{
		"property names differing in case":                 {"--property", "sample_id=S1", "--property", "Sample_ID=S2", "--property", "SAMPLE_ID=S3"},
		"property names in both unicode forms":             {"--property", nfc + "=1", "--property", nfd + "=2"},
		"property values in both unicode forms":            {"--property", "a=" + nfc, "--property", "b=" + nfd},
		"property names with zero-width characters":        {"--property", "a=1", "--property", "a" + zwsp + "=2", "--property", "a" + zwj + "=3", "--property", "a" + bom + "=4", "--property", "a" + shy + "=5"},
		"property names like lbf's":                        {"--property", "uploader" + zwsp + "=x", "--property", "run" + zwj + "=y", "--property", "source_path" + zwsp + "=z", "--property", "blob-location" + shy + "=w"},
		"property name of only a zero-width space":         {"--property", zwsp + "=x"},
		"property value of only a zero-width space":        {"--property", "a=" + zwsp},
		"property names in byte order":                     {"--property", "\U0001F600=1", "--property", "\uff21=2", "--property", "\ue000=3", "--property", "a=4", "--property", "B=5", "--property", "\u00c9=6", "--property", "_=7"},
		"properties file and a property in another case":   {"--properties", "props.json", "--property", "Sample_ID=x"},
		"properties file and a property in another form":   {"--properties", "props.json", "--property", nfd + "=w"},
		"properties file and the same property":            {"--properties", "props.json", "--property", nfc + "=w"},
		"instrument key with a zero-width space":           derived("name" + zwsp + "=a,version=1,url=https://a"),
		"instrument key in capitals":                       derived("NAME=a,version=1,url=https://a"),
		"instrument urls differing in case":                derived("name=a,version=1,url=https://example.org/Pipe", "name=b,version=1,url=https://example.org/pipe"),
		"instrument urls differing by a zero-width space":  derived("name=a,version=1,url=https://example.org/pipe", "name=b,version=1,url=https://example.org/pipe"+zwsp),
		"instrument names differing by a zero-width space": derived("name=align,version=1,url=https://example.org/pipe", "name=align"+zwsp+",version=1,url=https://example.org/pipe"),
		"instrument names in both unicode forms":           derived("name="+nfc+",version=1,url=https://example.org/pipe", "name="+nfd+",version=1,url=https://example.org/pipe"),
		"instrument versions differing by a soft hyphen":   derived("name=a,version=1.0,url=https://example.org/pipe", "name=a,version=1."+shy+"0,url=https://example.org/pipe"),
		"instrument userinfo with a kelvin sign":           derived("name=a,version=1,url=https://\u212a@example.org/"),
		"instrument port of only a zero-width space":       derived("name=a,version=1,url=https://[::1]" + zwsp + "/"),
		"parent with a zero-width space":                   {"--derived-from", datasetID + zwsp, "--instrument", "name=a,version=1,url=https://a"},
	} {
		cases["flags "+name] = conformCase{files: files, args: datasetDryRun(append([]string{"data"}, args...)...), normalize: []*regexp.Regexp{datasetModalVerb}}
	}
	for name, body := range map[string]string{
		"properties differing in case":           `{"properties": {"sample_id": "a", "Sample_ID": "b"}}`,
		"properties in both unicode forms":       `{"properties": {"` + nfc + `": "1", "` + nfd + `": "2"}}`,
		"duplicate properties in one form":       `{"properties": {"` + nfc + `": "1", "` + nfc + `": "2"}}`,
		"fields differing in case":               `{"name": "a", "NAME": "b"}`,
		"field with a zero-width space":          `{"name` + zwsp + `": "a"}`,
		"field with a kelvin sign":               `{"properties": {}, "name": "a", "` + "\u212a" + `ey": "b"}`,
		"name of only a zero-width space":        `{"name": "` + zwsp + `", "description": "` + shy + `"}`,
		"property named like lbf's":              `{"properties": {"source_path` + zwj + `": "x", "run` + zwsp + `": "y"}}`,
		"properties in byte order":               `{"properties": {"` + "\U0001F600" + `": "1", "` + "\uff21" + `": "2", "` + "\ue000" + `": "3", "z": "4", "` + "\u00c9" + `": "5", "a": "6", "B": "7"}}`,
		"instrument names in both unicode forms": `{"derived_from": "` + datasetID + `", "instruments": [{"name": "` + nfc + `", "version": "1", "url": "https://a"}, {"name": "` + nfd + `", "version": "1", "url": "https://a"}]}`,
		"instrument fields in capitals":          `{"derived_from": "` + datasetID + `", "instruments": [{"name": "a", "version": "1", "URL": "https://a"}]}`,
	} {
		cases["provenance file "+name] = conformCase{files: with(map[string]string{"p.json": body}), args: datasetDryRun("data", "--provenance", "p.json"), normalize: []*regexp.Regexp{datasetModalVerb}}
	}
	for name, body := range map[string]string{
		"keys differing in case":           `{"sample_id": "a", "Sample_ID": "b", "SAMPLE_ID": "c"}`,
		"keys in both unicode forms":       `{"` + nfc + `": "1", "` + nfd + `": "2"}`,
		"keys with zero-width characters":  `{"a": "1", "a` + zwsp + `": "2", "a` + bom + `": "3"}`,
		"duplicate keys differing in case": `{"a": "1", "A": "2", "a": "3"}`,
	} {
		cases["properties file "+name] = conformCase{files: with(map[string]string{"props.json": body}), args: datasetDryRun("data", "--properties", "props.json"), normalize: []*regexp.Regexp{datasetModalVerb}}
	}
	runDatasetCases(t, cases)
}

func TestConformanceStringsDatasetNames(t *testing.T) {
	cases := map[string]conformCase{
		"byte order": {files: stringsByteOrder, args: datasetDryRun("run1")},
		"byte order with case": {setup: func(t *testing.T, w *world) {
			datasetDistinct(t, w, "run1/A.txt", "run1/a.txt", "run1/B.txt", "run1/b.txt")
			for rel := range stringsByteOrder {
				datasetCreate(t, w, rel)
			}
		}, args: datasetDryRun("run1")},
		"zero-width names beside plain ones": {files: (map[string]string{"run1/ab.txt": "1", "run1/a" + zwsp + "b.txt": "2", "run1/a" + zwj + "b.txt": "3",
			"run1/a" + bom + "b.txt": "4", "run1/a" + shy + "b.txt": "5"}), args: datasetDryRun("run1")},
		"names differing by case and a zero-width space": {files: (map[string]string{"run1/A" + zwsp + ".txt": "1", "run1/a.txt": "2"}), args: datasetDryRun("run1")},
		"folders of only zero-width characters": {files: (map[string]string{"run1/" + zwsp + "/x.txt": "1", "run1/" + zwj + "/y.txt": "2", "run1/x.txt": "3"}),
			args: datasetDryRun("run1")},
		"dot folders with zero-width characters": {files: (map[string]string{"run1/." + zwsp + "/x.txt": "1", "run1/.." + zwsp + "/y.txt": "2", "run1/z.txt": "3"}),
			args: datasetDryRun("run1")},
		"excluded names with zero-width characters": {files: (map[string]string{"run1/.DS_Store" + zwsp: "1", "run1/" + zwsp + "._x": "2", "run1/a.txt": "3"}),
			args: datasetDryRun("run1")},
		"crate name with a zero-width space":         {files: (map[string]string{crateName + zwsp: "{}"}), args: datasetDryRun(crateName + zwsp)},
		"crate name with a zero-width space folder":  {files: (map[string]string{crateName + zwj + "/a.txt": "a"}), args: datasetDryRun(crateName + zwj)},
		"input folder of only a zero-width space":    {files: (map[string]string{zwsp + "/a.txt": "a"}), args: datasetDryRun(zwsp)},
		"input through a zero-width folder and back": {files: (map[string]string{"data/a.txt": "a"}), args: datasetDryRun(zwsp + "/../data")},
		"input through a dot and zero-width folder":  {files: (map[string]string{"data/a.txt": "a", "." + zwsp + "/b.txt": "b"}), args: datasetDryRun("." + zwsp + "/../data")},
		"media types with zero-width characters":     {files: (map[string]string{"run1/a.txt" + zwsp: "1", "run1/b.TXT": "2", "run1/c.t" + shy + "xt": "3"}), args: datasetDryRun("run1")},
	}
	for name, pair := range map[string][2]string{
		"unicode forms of folders": {"run1/" + nfc + "/x.txt", "run1/" + nfd + "/y.txt"},
		"case of folders and form": {"run1/CAF\u00c9/x.txt", "run1/" + nfd + "/y.txt"},
	} {
		cases["same name "+name] = conformCase{setup: func(t *testing.T, w *world) { datasetDistinct(t, w, pair[0], pair[1]) }, args: datasetDryRun("run1")}
	}
	runDatasetCases(t, cases)
}

func TestConformanceStringsTransfer(t *testing.T) {
	ids := []*regexp.Regexp{reDone, reCount}
	odd := map[string]string{}
	for rel, body := range stringsByteOrder {
		odd["data/"+rel] = body
	}
	for rel, body := range map[string]string{
		"run1/ab.txt": "plain", "run1/a" + zwsp + "b.txt": "zero-width", "run1/." + zwsp + "/x.txt": "dot", "run1/.." + zwsp + "/y.txt": "dot dot",
		"run1/" + zwsp + "/z.txt": "only zero-width", "run1/" + nfc + ".txt": "composed",
	} {
		odd["data/"+rel] = body
	}
	props := []string{"--property", "sample_id=S1", "--property", "Sample_ID=S2", "--property", nfc + "=composed", "--property", nfd + "=decomposed",
		"--property", "\U0001F600=1", "--property", "\uff21=2", "--property", "\ue000=3", "--property", "source_path" + zwsp + "=x"}
	publish := func(extra ...string) []string {
		return append([]string{"publish", "data/run1", "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID}, extra...)
	}
	fetch := []string{"fetch", fixedID, "--sas-env", "{sas}", "--container", "{container}", "--out", "out"}
	fetchJSON := append(append([]string{}, fetch...), "--json")
	zeroWidthFolder := map[string]string{"data/" + zwsp + "/a.txt": "a", "data/" + zwsp + "/b/c.txt": "c"}
	publishZeroWidth := func(t *testing.T, w *world) {
		goOK(t, w, "publish", "data/"+zwsp, "--sas-env", "{sas}", "--container", "{container}", "--id", fixedID)
	}
	cases := map[string]conformCase{
		"publish odd names":       {perms: "rlw", files: odd, args: publish(props...), normalize: ids, unorderedStderr: true},
		"fetch odd names":         {perms: "rlw", files: odd, setup: goPublish(fixedID, props...), args: fetchJSON, normalize: ids, unorderedStderr: true},
		"fetch odd names again":   {perms: "rlw", files: odd, setup: steps(goPublish(fixedID, props...), func(t *testing.T, w *world) { goOK(t, w, fetch...) }), args: fetchJSON, normalize: ids},
		"publish odd names again": {perms: "rlw", files: odd, setup: goPublish(fixedID, props...), args: publish(props...), normalize: ids, unorderedStderr: true},
		"fetch zero-width folder": {perms: "rlw", files: zeroWidthFolder, setup: publishZeroWidth, args: fetchJSON, normalize: ids, unorderedStderr: true},
		"fetch zero-width folder again": {perms: "rlw", files: zeroWidthFolder, setup: steps(publishZeroWidth, func(t *testing.T, w *world) { goOK(t, w, fetch...) }),
			args: fetchJSON, normalize: ids},
		"republish other value form": {perms: "rlw", files: roundTripFiles, setup: goPublish(fixedID, "--property", "note="+nfc),
			args: publish("--property", "note="+nfd), normalize: ids, unorderedStderr: true},
		"republish name with a zero-width space": {perms: "rlw", files: roundTripFiles, setup: goPublish(fixedID, "--name", "Plate 1"),
			args: publish("--name", "Plate"+zwsp+" 1"), normalize: ids, unorderedStderr: true},
		"republish cased property": {perms: "rlw", files: roundTripFiles, setup: goPublish(fixedID, "--property", "sample_id=S1"),
			args: publish("--property", "Sample_ID=S1"), normalize: ids, unorderedStderr: true},
		"republish tool with a soft hyphen": {perms: "rlw", files: roundTripFiles,
			setup: goPublish(fixedID, "--derived-from", parentDatasetID, "--instrument", "name=CellProfiler,version=4.2.6,url=https://cellprofiler.org"),
			args:  publish("--derived-from", parentDatasetID, "--instrument", "name=Cell"+shy+"Profiler,version=4.2.6,url=https://cellprofiler.org"), normalize: ids, unorderedStderr: true},
		"fetch a name like the folder's": {perms: "rlw", files: roundTripFiles, setup: goPublish(fixedID, "--name", "run1"+zwsp, "--description", "Dataset of 8 files from run1"+zwj),
			args: fetchJSON, normalize: ids, unorderedStderr: true},
		"fetch an id in capitals": {perms: "rlw", files: roundTripFiles, setup: goPublish(fixedID),
			args: []string{"fetch", strings.ToUpper(fixedID), "--sas-env", "{sas}", "--container", "{container}", "--out", "out"}},
		"container with a zero-width space": {perms: "rl", args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}" + zwsp}},
		"credential values of only zero-width characters": {perms: "rlw", files: roundTripFiles, setup: func(t *testing.T, w *world) {
			setSASEnv(t, w, "USER_IDENTITY", zwsp)
			setSASEnv(t, w, "SUBSCRIPTION_NAME", zwj)
			setSASEnv(t, w, "SUBSCRIPTION_ID", bom)
		}, args: publish(), normalize: ids, unorderedStderr: true},
		"check with a user of only a zero-width space": {perms: "rl", setup: func(t *testing.T, w *world) { setSASEnv(t, w, "USER_IDENTITY", zwsp) },
			args: []string{"check", "--mode", "download", "--sas-env", "{sas}", "--container", "{container}"}, normalize: []*regexp.Regexp{reExpiry}},
	}
	conformAll(t, cases)
}

func TestConformanceStringsFetchCommand(t *testing.T) {
	cases := [][4]string{
		{"X", "bronze" + zwsp, "tag=storage" + zwsp, "acc"},
		{"X", "bronze" + shy, "tag=storage" + bom, "acc"},
		{"X", "BRONZE", "TAG=storage", "acc"},
		{"X", nfd, "tag=" + nfc, "acc" + zwj},
	}
	var calls []string
	for _, c := range cases {
		calls = append(calls, fmt.Sprintf("Get-LbfFetchCommand %s %s %s %s", psQuote(c[0]), psQuote(c[1]), psQuote(c[2]), psQuote(c[3])))
	}
	r := psCall(t, nil, strings.Join(calls, "\n"))
	got := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	for i, c := range cases {
		if want := fetchCommand(c[0], c[1], c[2], c[3]); i >= len(got) || got[i] != want {
			t.Errorf("fetchCommand%q: go %q, ps %q", c, want, got)
		}
	}
}

func TestConformanceStringsChooseAccount(t *testing.T) {
	type choiceCase struct {
		Want    string `json:"want"`
		Matches int    `json:"matches"`
	}
	var all []accountMatch
	for _, name := range []string{"\uff21", "\U0001F600", "a", "\ue000", "b" + zwsp, "A"} {
		all = append(all, accountMatch{Name: name, Location: "uksouth", Sub: &armsubscriptions.Subscription{DisplayName: new("Sub " + name), SubscriptionID: new("id-" + name)}})
	}
	cases := []choiceCase{{"", 6}, {zwsp, 6}, {"A", 3}, {"b", 6}, {"b" + zwsp, 6}, {"a" + shy, 6}, {"\uff41", 6}}
	var want []string
	for _, c := range cases {
		got, err := chooseAccount(append([]accountMatch(nil), all[:c.Matches]...), "tag=x", c.Want, nil)
		if err != nil {
			want = append(want, "ERR "+err.Error())
		} else {
			want = append(want, got.Name)
		}
	}
	var psMatches []map[string]string
	for _, a := range all {
		psMatches = append(psMatches, map[string]string{"Name": a.Name, "Location": a.Location, "SubscriptionName": *a.Sub.DisplayName, "SubscriptionID": *a.Sub.SubscriptionID})
	}
	stdout, _ := psEval(t, `
$out = foreach ($c in @($data.cases)) {
    $list = @()
    $i = 0
    foreach ($m in @($data.matches)) { if ($i -lt $c.matches) { $list += @{ Name = $m.Name; Location = $m.Location; SubscriptionName = $m.SubscriptionName; SubscriptionID = $m.SubscriptionID } }; $i++ }
    try { (Select-LbfAccount -Matches $list -Tag 'tag=x' -Want $c.want -Picker $null).Name }
    catch { 'ERR ' + (Get-LbfErrorText $_) }
}
ConvertTo-LbfJson ([object[]]@($out))
`, map[string]any{"cases": cases, "matches": psMatches})
	var got []string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || len(got) != len(cases) {
		t.Fatalf("PowerShell printed %q (%v)", stdout, err)
	}
	for i, c := range cases {
		if got[i] != want[i] {
			t.Errorf("%+v:\nps %q\ngo %q", c, got[i], want[i])
		}
	}
}

func TestConformanceStringsFlattenCrate(t *testing.T) {
	powershell(t)
	ctx := `"@context": "` + roCrateContext + `"`
	crates := []string{
		`{` + ctx + `, "@graph": [{"@id": "./", "@type": "Dataset", "identifier": "X", "name` + zwsp + `": "zw", "hasPart": [{"@id": "a"}, {"@id": "b"}],
			"additionalProperty": [{"@id": "#p1"}, {"@id": "#p2"}, {"@id": "#p3"}, {"@id": "#p4"}]},
			{"@id": "#p1", "@type": "PropertyValue", "name": "sample_id", "value": "1"}, {"@id": "#p2", "@type": "PropertyValue", "name": "Sample_ID", "value": "2"},
			{"@id": "#p3", "@type": "PropertyValue", "name": "` + nfc + `", "value": "3"}, {"@id": "#p4", "@type": "PropertyValue", "name": "` + nfd + `", "value": "4"},
			{"@id": "a", "@type": "File", "contentSize": "1", "sha256": "` + zwsp + `"}, {"@id": "b", "@type": "File` + zwsp + `", "contentSize": "2"}]}`,
		`{` + ctx + `, "@graph": [{"@id` + zwsp + `": "./", "@type": "Dataset"}, {"@id": "./", "@type": "Dataset", "identifier": "Y", "hasPart` + zwj + `": [{"@id": "a"}],
			"hasPart": {"@id": "a"}, "additionalProperty` + zwj + `": {"@id": "#p"}, "mentions": {"@id": "#run"}, "isBasedOn": {"@id": "#source-Z"}},
			{"@id": "#source-Z", "@type": "Dataset", "identifier": "Z", "name": "Z` + shy + `"},
			{"@id": "#run", "@type": "CreateAction` + bom + `", "result": {"@id": "./"}, "instrument": {"@id": "https://t"}},
			{"@id": "https://t", "@type": "SoftwareApplication", "name": "https://t` + zwsp + `"}, {"@id": "a", "@type": "File", "contentSize": "1"}]}`,
		`{` + ctx + `, "@graph": [{"@id": "./", "@type": "Dataset", "identifier": "W", "hasPart": {"@id": "a"},
			"keywords": {"` + "\uff21" + `": "1", "` + "\U0001F600" + `": "2", "` + "\ue000" + `": "3", "a": "4", "A": "5"}, "creator": {"@id": "#u"}},
			{"@id": "#u", "@type": "Person", "name": "#u` + zwsp + `"}, {"@id": "a", "@type": "File", "contentSize": "1", "name": "` + nfd + `"}]}`,
	}
	dir := t.TempDir()
	var paths []string
	for i, c := range crates {
		p := filepath.Join(dir, fmt.Sprintf("%02d.json", i))
		must(t, os.WriteFile(p, []byte(c), 0o644))
		paths = append(paths, psQuote(p))
	}
	r := psCall(t, nil, `$out = foreach ($p in @(`+strings.Join(paths, ",")+`)) {
		try {
			$f = ConvertFrom-LbfBrowseCrate ($script:Utf8NoBom.GetString([IO.File]::ReadAllBytes($p)))
			ConvertTo-LbfJson @{ row = $f.Row; files = $f.Files; names = $f.Names }
		} catch { 'ERR ' + (Get-LbfErrorText $_) }
	}
	ConvertTo-LbfJson ([object[]]@($out))`)
	var got []string
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil || len(got) != len(crates) {
		t.Fatalf("PowerShell printed %q (%v)\nstderr: %s", r.stdout, err, r.stderr)
	}
	for i, c := range crates {
		if want := goFlattened([]byte(c)); got[i] != want {
			t.Errorf("crate %d:\n go: %s\n ps: %s", i, want, got[i])
		}
	}
}

// encoding/json matches a saved login's fields by Unicode case folding, under which the long s is s and dotted I only itself.
func TestConformanceStringsAuthRecord(t *testing.T) {
	powershell(t)
	// Built before HOME moves, since building may run go, which keeps its module cache under HOME.
	psScriptVersion(t, "dev")
	home := t.TempDir()
	env := map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"), "APPDATA": filepath.Join(home, "AppData")}
	for k, v := range env {
		t.Setenv(k, v)
	}
	path, err := recordPath()
	must(t, err)
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	record := func(user, version string) string {
		return `{"authority": "login.microsoftonline.com", "clientId": "c", "homeAccountId": "h", "tenantId": "t", ` + user + `, "version": "` + version + `"}`
	}
	for _, body := range []string{
		record(`"username": "ann@example.org"`, "1.0"),
		record(`"USERNAME": "ann@example.org"`, "1.0"),
		record(`"u`+"\u017f"+`ername": "ann@example.org"`, "1.0"),
		record(`"user`+zwsp+`name": "ann@example.org"`, "1.0"),
		record(`"username": "a", "Username": "b"`, "1.0"),
		record(`"username": "ann@example.org"`, "1.0"+zwsp),
		record(`"username": "ann@example.org"`, zwsp),
		`{"cl` + "\u0130" + `entId": "c", "username": "ann@example.org", "version": "1.0", "tenantId": "` + nfd + `"}`,
	} {
		must(t, os.WriteFile(path, []byte(body), 0o600))
		want := "none"
		if rec, ok := loadRecord(); ok {
			want = fmt.Sprintf("%q %q %q", rec.Username, rec.ClientID, rec.TenantID)
		}
		r := psCall(t, env, `$r = Read-LbfAuthRecord
if ($null -eq $r) { 'none' } else { '{0} {1} {2}' -f (Format-LbfQuote $r.Username), (Format-LbfQuote $r.ClientId), (Format-LbfQuote $r.TenantId) }`)
		if got := strings.TrimSuffix(r.stdout, "\n"); got != want {
			t.Errorf("%s:\n go %s\n ps %s", body, want, got)
		}
	}
}

func TestConformanceStringsProfiles(t *testing.T) {
	head := `{"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "https://example.org/x", `
	props := func(schema string) string {
		return head + `"properties": {"crate": {"properties": {"additionalProperty": ` + schema + `}}}}`
	}
	format := func(name string) string { return props(`{"additionalProperties": {"format": "` + name + `"}}`) }
	kelvin := "K"
	cases := map[string]conformCase{}
	for name, c := range map[string]struct {
		profile string
		args    []string
	}{
		"title keys differing in case":        {`{"$id": "https://example.org/x", "title": "lower", "TITLE": "Upper", "Title": null}`, nil},
		"title with a dotless i":              {`{"$id": "https://example.org/x", "title": "lower", "t` + "ı" + `tle": "dotless"}`, nil},
		"id of only a zero-width space":       {`{"$id": "` + zwsp + `"}`, nil},
		"id key in capitals":                  {`{"$ID": 5}`, nil},
		"id host of only a zero-width space":  {`{"$id": "https://` + zwsp + `/x"}`, nil},
		"id scheme with a kelvin sign":        {`{"$id": "` + kelvin + `a:b"}`, nil},
		"negated posix class":                 {props(`{"additionalProperties": {"pattern": "^[[:^alpha:]]+$"}}`), []string{"--property", "a=12é", "--property", "b=x"}},
		"posix class in capitals":             {props(`{"additionalProperties": {"pattern": "^[[:ALPHA:]]+$"}}`), []string{"--property", "a=x"}},
		"pattern flag in capitals":            {props(`{"additionalProperties": {"pattern": "(?I)x"}}`), []string{"--property", "a=x"}},
		"required names with curly quotes":    {props(`{"required": ["it` + "’" + `s", "o` + "‘" + `k"]}`), []string{"--property", "it’s=1"}},
		"property names with curly quotes":    {props(`{"properties": {"it` + "’" + `s": {"maxLength": 1}}}`), []string{"--property", "it’s=12"}},
		"required name in another case":       {props(`{"required": ["Sample_ID"]}`), []string{"--property", "sample_id=1"}},
		"required name in another form":       {props(`{"required": ["` + nfc + `"]}`), []string{"--property", nfd + "=1"}},
		"property schema in another case":     {props(`{"properties": {"Sample_ID": {"maxLength": 1}}}`), []string{"--property", "sample_id=12"}},
		"enum value in another form":          {props(`{"additionalProperties": {"enum": ["` + nfc + `"]}}`), []string{"--property", "a=" + nfd}},
		"const value with a zero-width space": {props(`{"additionalProperties": {"const": "ab"}}`), []string{"--property", "a=a" + zwsp + "b"}},
		"additional properties in byte order": {props(`{"additionalProperties": false}`), []string{"--property", "Ａ=1"}},
		"additional emoji property":           {props(`{"additionalProperties": false}`), []string{"--property", "\U0001F600=2"}},
		"json-pointer of a zero-width space":  {format("json-pointer"), []string{"--property", "p=" + zwsp}},
		"time with a zero-width space":        {format("time"), []string{"--property", "t=12:00:00z" + zwsp}},
		"hostname with a kelvin sign":         {format("hostname"), []string{"--property", "h=" + kelvin + "a.org"}},
		"hostname with an empty label":        {format("hostname"), []string{"--property", "h=a..b"}},
		"hostname with a long label":          {format("hostname"), []string{"--property", "h=" + strings.Repeat("x", 64)}},
		"email with an empty label":           {format("email"), []string{"--property", "e=a@b..c"}},
		"uuid with a kelvin sign":             {format("uuid"), []string{"--property", "u=0000000" + kelvin + "-0000-0000-0000-000000000000"}},
		"uuid with an emoji":                  {format("uuid"), []string{"--property", "u=0000\U0001F600-0000-0000-0000-000000000000"}},
		"uuid with a g":                       {format("uuid"), []string{"--property", "u=g0000000-0000-0000-0000-000000000000"}},
		"format name with a zero-width space": {format("uuid" + zwsp), []string{"--property", "u=zz"}},
		"format name in capitals":             {format("UUID"), []string{"--property", "u=zz"}},
		"unique items given as a string":      {props(`{"properties": {"a": {"uniqueItems": "true"}}}`), []string{"--property", "a=x"}},
		"type name with a zero-width space":   {props(`{"properties": {"a": {"type": "string` + zwsp + `"}}}`), []string{"--property", "a=x"}},
	} {
		files := map[string]string{"run1/a.txt": "a", "profiles/x/profile.json": c.profile}
		cases[name] = conformCase{files: files, args: datasetDryRun(append([]string{"run1", "--profile", "profiles/x"}, c.args...)...),
			normalize: []*regexp.Regexp{datasetAdditional, datasetModalVerb}}
	}
	runDatasetCases(t, cases)
}
