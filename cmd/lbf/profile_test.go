package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const plateReadProfile = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.org/profiles/plate-read/0.1.0",
  "$ref": "https://github.com/ImperialCollegeLondon/lbf-cli/tree/main/profiles/bronze/0.7.0",
  "title": "Plate read",
  "properties": {
    "hasPart": {"description": "The reader's export, saved as readings.csv", "contains": {"properties": {"@id": {"pattern": "(^|/)readings\\.csv$"}}}}
  },
  "$defs": {
    "properties": {
      "type": "object",
      "required": ["plate_id"],
      "additionalProperties": false,
      "properties": {
        "plate_id": {"type": "string", "title": "Plate barcode", "description": "On the side of the plate", "examples": ["P-0001"], "pattern": "^P-[0-9]{4}$"},
        "wavelength_nm": {"type": "integer", "title": "Wavelength", "description": "In nanometres", "examples": [450], "minimum": 200, "maximum": 1000,
          "x-unitCode": "http://qudt.org/vocab/unit/NanoM", "x-unitText": "nm"},
        "lidded": {"type": "boolean", "title": "Lidded", "description": "Whether the plate was read with its lid on", "examples": [true]}
      }
    }
  }
}`

const roughProfile = `{
  "$id": "https://example.org/profiles/rough/0.1.0",
  "$ref": "https://github.com/ImperialCollegeLondon/lbf-cli/tree/main/profiles/bronze/0.7.0",
  "titel": "Rough",
  "properties": {"additionalProperty": {"$ref": "#/$defs/properties"}},
  "$defs": {"properties": {"required": ["a", "b"], "properties": {
    "a": {"title": "A", "description": "a", "examples": ["x"]},
    "b": {"type": "string", "default": "x", "minLength": 3, "examples": ["xy"], "x-unitText": "nm"},
    "c": {"type": "number", "title": "C", "description": "c", "examples": [1], "x-unitCode": "nm", "x-unitText": "nm"},
    "d": {"type": "object", "title": "D", "description": "d", "examples": [{}]}
  }}}
}`

func plateRead(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "profiles", "plate-read", "0.1.0")
	writeFile(t, filepath.Join(dir, "profile.json"), plateReadProfile)
	return dir
}

func plateFlags(props ...string) provenanceFlags { return provenanceFlags{props: props} }

func crateGraph(t *testing.T, raw []byte) map[string]map[string]any {
	t.Helper()
	var doc struct {
		Graph []map[string]any `json:"@graph"`
	}
	must(t, json.Unmarshal(raw, &doc))
	byID := map[string]map[string]any{}
	for _, e := range doc.Graph {
		byID[e["@id"].(string)] = e
	}
	return byID
}

func TestPropertiesAreCheckedAsTheRecordGiven(t *testing.T) {
	data := dataset(t, map[string]string{"readings.csv": "a,b"})
	tgt := target{User: "u@example.org"}
	for name, c := range map[string]struct {
		props []string
		want  []string
	}{
		"pattern":      {[]string{"plate_id=X1"}, []string{"--property plate_id: 'X1' is not in the form the profile asks for", "Plate barcode; On the side of the plate; e.g. P-0001"}},
		"range":        {[]string{"plate_id=P-0001", "wavelength_nm=9000"}, []string{"--property wavelength_nm: is 9000, but must be at most 1000", "Wavelength; In nanometres; e.g. 450"}},
		"not a number": {[]string{"plate_id=P-0001", "wavelength_nm=blue"}, []string{"--property wavelength_nm: is text, but must be a whole number"}},
		"not true":     {[]string{"plate_id=P-0001", "lidded=yes"}, []string{"--property lidded: is text, but must be true or false"}},
		"missing":      {nil, []string{"missing --property plate_id=...", "Plate barcode"}},
		"unasked":      {[]string{"plate_id=P-0001", "colour=red"}, []string{"--property colour: not a property the profile asks for"}},
	} {
		pub, err := preparePublication(data, plateFlags(c.props...), []string{plateRead(t)}, "")
		must(t, err)
		_, err = pub.crate(tgt)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.HasPrefix(err.Error(), "dataset does not meet profile Plate read (https://example.org/profiles/plate-read/0.1.0):") {
			t.Errorf("%s: heading %v", name, err)
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q not in\n%v", name, w, err)
			}
		}
	}

	pub, err := preparePublication(dataset(t, map[string]string{"other.csv": "a"}), plateFlags("plate_id=P-0001"), []string{plateRead(t)}, "")
	must(t, err)
	if _, err := pub.crate(tgt); err == nil || !strings.Contains(err.Error(), "files: no file is what the profile asks for\n      The reader's export, saved as readings.csv") {
		t.Errorf("files: %v", err)
	}
}

func TestTypedPropertiesCarryTheirUnits(t *testing.T) {
	pub, err := preparePublication(dataset(t, map[string]string{"readings.csv": "a,b"}), plateFlags("plate_id=P-0001", "wavelength_nm=450", "lidded=false"), []string{plateRead(t)}, "")
	must(t, err)
	raw, err := pub.crate(target{User: "u@example.org"})
	must(t, err)
	g := crateGraph(t, raw)
	if pv := g["#wavelength_nm"]; pv["value"] != float64(450) || pv["unitCode"] != "http://qudt.org/vocab/unit/NanoM" || pv["unitText"] != "nm" {
		t.Errorf("wavelength_nm: %v", pv)
	}
	if pv := g["#lidded"]; pv["value"] != false || pv["unitCode"] != nil {
		t.Errorf("lidded: %v", pv)
	}
	if pv := g["#plate_id"]; pv["value"] != "P-0001" {
		t.Errorf("plate_id: %v", pv)
	}
	c, err := readCrate(raw)
	must(t, err)
	stated, _, err := c.statement()
	must(t, err)
	if stated.Properties["wavelength_nm"] != "450" || stated.Properties["lidded"] != "false" {
		t.Errorf("read back as %v", stated.Properties)
	}
}

func TestDatasetKeepsEachSchemaItMet(t *testing.T) {
	pub, err := preparePublication(dataset(t, map[string]string{"readings.csv": "a,b"}), plateFlags("plate_id=P-0001"), []string{plateRead(t)}, "")
	must(t, err)
	raw, err := pub.crate(target{User: "u@example.org"})
	must(t, err)
	g := crateGraph(t, raw)
	var parts []string
	for _, p := range asList(g["./"]["hasPart"]) {
		parts = append(parts, refID(p))
	}
	want := []string{"run1/readings.csv", "ro-crate-profiles/bronze/0.7.0/profile.json", "ro-crate-profiles/plate-read/0.1.0/profile.json"}
	if !slices.Equal(parts, want) {
		t.Fatalf("hasPart %v", parts)
	}
	file := g["ro-crate-profiles/plate-read/0.1.0/profile.json"]
	if file["encodingFormat"] != "application/schema+json" || refID(file["conformsTo"]) != "https://json-schema.org/draft/2020-12/schema" || file["sha256"] == nil {
		t.Errorf("schema file %v", file)
	}
	prof := g["https://example.org/profiles/plate-read/0.1.0"]
	desc := g[refID(prof["hasResource"])]
	if refID(prof["isProfileOf"]) != bronzeProfileID(t) || !slices.Contains(typesOf(desc), "ResourceDescriptor") || refID(desc["hasArtifact"]) != "ro-crate-profiles/plate-read/0.1.0/profile.json" || refID(desc["hasRole"]) != roleSchema {
		t.Errorf("profile %v, descriptor %v", prof, desc)
	}
	if !strings.Contains(g["./"]["description"].(string), "Dataset of 1 file from run1") {
		t.Errorf("description counts the schemas: %v", g["./"]["description"])
	}
}

func TestPublishedSchemasAreFetchedBesideTheData(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	id := newID()
	pub, err := preparePublication(dataset(t, map[string]string{"readings.csv": "a,b"}), plateFlags("plate_id=P-0001"), []string{plateRead(t)}, id)
	must(t, err)
	must(t, upload(ctx, tg, pub))
	stored := storedNow(t, tg, id)
	if _, ok := stored["ro-crate-profiles/plate-read/0.1.0/profile.json"]; !ok {
		t.Fatalf("stored %v", stored)
	}
	got, err := download(ctx, tg, id, t.TempDir())
	must(t, err)
	if got.dataPath != filepath.Join(got.path, "run1") {
		t.Errorf("data_path %s", got.dataPath)
	}
	body, err := os.ReadFile(filepath.Join(got.path, profilesDir, "plate-read", "0.1.0", "profile.json"))
	if err != nil || string(body) != plateReadProfile {
		t.Errorf("fetched schema %q %v", body, err)
	}

	again, err := preparePublication(dataset(t, map[string]string{"readings.csv": "a,b"}), plateFlags("plate_id=P-0001"), []string{plateRead(t)}, id)
	must(t, err)
	must(t, upload(ctx, tg, again))

	changed := filepath.Join(t.TempDir(), "profiles", "plate-read", "0.1.0")
	writeFile(t, filepath.Join(changed, "profile.json"), strings.Replace(plateReadProfile, `"Plate read"`, `"Plate reading"`, 1))
	other, err := preparePublication(dataset(t, map[string]string{"readings.csv": "a,b"}), plateFlags("plate_id=P-0001"), []string{changed}, id)
	must(t, err)
	err = upload(ctx, tg, other)
	if _, ok := errors.AsType[idTaken](err); !ok || !strings.Contains(err.Error(), "ro-crate-profiles/plate-read/0.1.0/profile.json") {
		t.Fatalf("another schema under the same ID: %v", err)
	}
}

func TestSeveralProfiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "profiles", "minimal-silver", "0.1.0", "profile.json"), minimalSilverProfile)
	writeFile(t, filepath.Join(root, "profiles", "cell-painting", "0.1.0", "profile.json"), cellPaintingProfile)
	writeFile(t, filepath.Join(root, "profiles", "plate-read", "0.1.0", "profile.json"), plateReadProfile)
	dir := func(name string) string { return filepath.Join(root, "profiles", name, "0.1.0") }
	prof, err := loadProfiles([]string{dir("cell-painting"), dir("plate-read"), dir("minimal-silver")})
	must(t, err)
	want := []string{bronzeProfileID(t), "https://example.org/profiles/minimal-silver/0.1.0", "https://example.org/profiles/cell-painting/0.1.0", "https://example.org/profiles/plate-read/0.1.0"}
	if got := prof.ids(); !slices.Equal(got, want) {
		t.Fatalf("chain %v", got)
	}

	elsewhere := filepath.Join(t.TempDir(), "profiles", "plate-read", "0.1.0")
	writeFile(t, filepath.Join(elsewhere, "profile.json"), strings.Replace(plateReadProfile, `"Plate read"`, `"Plate"`, 1))
	if _, err := loadProfiles([]string{dir("plate-read"), elsewhere}); err == nil || !strings.Contains(err.Error(), "two copies of https://example.org/profiles/plate-read/0.1.0 differ") {
		t.Errorf("differing copies: %v", err)
	}
	clash := filepath.Join(t.TempDir(), "profiles", "plate-read", "0.1.0")
	writeFile(t, filepath.Join(clash, "profile.json"), strings.Replace(plateReadProfile, "https://example.org/profiles/plate-read", "https://example.com/plate-read", 1))
	if _, err := loadProfiles([]string{dir("plate-read"), clash}); err == nil || !strings.Contains(err.Error(), "are both plate-read 0.1.0") {
		t.Errorf("same name and version: %v", err)
	}
}

func TestAFolderNamedLikeTheSchemasIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Ro-Crate-Profiles")
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	if _, err := preparePublication(dir, provenanceFlags{}, nil, ""); err == nil || !strings.Contains(err.Error(), "which lbf keeps for the schemas") {
		t.Fatalf("got %v", err)
	}
}

func TestAProfileOnAnotherBronzeSaysWhatToChange(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "silver", "0.1.0")
	writeFile(t, filepath.Join(dir, "profile.json"), `{"$id": "https://example.org/profiles/silver/0.1.0", "$ref": "https://github.com/ImperialCollegeLondon/lbf-data-tools/tree/main/profiles/bronze/0.4.0"}`)
	_, err := loadProfile(dir)
	if err == nil || !strings.Contains(err.Error(), "builds on bronze 0.4.0, but this lbf checks bronze 0.7.0") || !strings.Contains(err.Error(), "whose $ref is "+bronzeProfileID(t)) {
		t.Fatalf("got %v", err)
	}
}

func TestProfilesCheck(t *testing.T) {
	_, problems, err := checkProfile(plateRead(t))
	must(t, err)
	if len(problems) > 0 {
		t.Fatalf("a ready profile: %v", problems)
	}

	dir := filepath.Join(t.TempDir(), "profiles", "rough", "0.1.0")
	writeFile(t, filepath.Join(dir, "profile.json"), roughProfile)
	_, problems, err = checkProfile(dir)
	must(t, err)
	for _, want := range []string{
		"/: titel is not a JSON Schema keyword, so nothing checks it; did you mean title?",
		"/properties/additionalProperty: the crate's additionalProperty holds schema.org PropertyValues",
		`property a has no type; give "string", "integer", "number" or "boolean", or a list of choices`,
		"property b has no title, which a form shows as its label",
		"property b is required, so a default would answer for whoever publishes; leave it out",
		"property b: example xy does not meet the property's own rules",
		"property b gives only one of x-unitCode and x-unitText",
		"property c has x-unitCode nm, but it must be a unit's URL",
		"property d has type object, but a property's value is text, a number or true/false",
	} {
		if !slices.Contains(problems, want) && !slices.ContainsFunc(problems, func(p string) bool { return strings.HasPrefix(p, want) }) {
			t.Errorf("missing %q in\n%s", want, strings.Join(problems, "\n"))
		}
	}
}
