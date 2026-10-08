package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
)

// Published profiles live in the storage account beside the datasets that meet them, as <name>/<version>/profile.json.
const profilesContainer = "profiles"

var (
	profileRef  = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*)(?:@(v?[0-9]+(?:\.[0-9]+)*))?$`)
	profileSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// A --profile names a published profile unless it is a path, or a profile.json, or a folder holding one, here.
func registryRef(arg string) (name, version string, ok bool) {
	if arg == "" || strings.ContainsAny(arg, `/\`) {
		return "", "", false
	}
	if info, err := os.Stat(arg); err == nil && !info.IsDir() {
		return "", "", false
	}
	if _, err := os.Stat(filepath.Join(arg, "profile.json")); err == nil {
		return "", "", false
	}
	m := profileRef.FindStringSubmatch(arg)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// The name and version a profile is published under: the last two segments of its $id.
func profilePath(id string) (name, version string, err error) {
	u, err := url.Parse(id)
	if err == nil {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) >= 2 && profileSlug.MatchString(segs[len(segs)-2]) && versionFolder.MatchString(segs[len(segs)-1]) {
			return segs[len(segs)-2], segs[len(segs)-1], nil
		}
	}
	return "", "", fmt.Errorf("$id %s does not end in /<name>/<version>, such as https://w3id.org/lbf/profiles/plate-read/0.1.0, so it cannot be published", id)
}

func versionLess(a, b string) bool {
	parse := func(v string) []int {
		var out []int
		for s := range strings.SplitSeq(strings.TrimPrefix(v, "v"), ".") {
			n, _ := strconv.Atoi(s)
			out = append(out, n)
		}
		return out
	}
	return slices.Compare(parse(a), parse(b)) < 0
}

var cacheRoot = os.UserCacheDir

func profilesCache(account string) (string, error) {
	root, err := cacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "lbf", "profiles", account), nil
}

// Copies every published profile not yet cached; a published version never changes, so a cached one is never fetched again.
func syncProfiles(ctx context.Context, t target) (string, error) {
	dir, err := profilesCache(t.Account)
	if err != nil {
		return "", err
	}
	cc, err := t.client()
	if err != nil {
		return "", err
	}
	status("Reading the profiles in " + t.Account)
	defer status("")
	pager := cc.NewListBlobsFlatPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if bloberror.HasCode(err, bloberror.ContainerNotFound) {
			return "", fmt.Errorf("%s has no %q container, so it has no published profiles; whoever looks after the storage account creates it, then 'lbf profiles publish' fills it", t.Account, profilesContainer)
		}
		if err != nil {
			return "", fmt.Errorf("listing %s/%s: %w", t.Account, profilesContainer, err)
		}
		for _, b := range page.Segment.BlobItems {
			segs := strings.Split(*b.Name, "/")
			if len(segs) != 3 || segs[2] != "profile.json" || !profileSlug.MatchString(segs[0]) || !versionFolder.MatchString(segs[1]) {
				continue
			}
			local := filepath.Join(dir, segs[0], segs[1], "profile.json")
			if _, err := os.Stat(local); err == nil {
				continue
			}
			raw, err := downloadBuffer(ctx, cc, *b.Name)
			if err != nil {
				return "", fmt.Errorf("downloading %s/%s/%s: %w", t.Account, profilesContainer, *b.Name, err)
			}
			if err := writeAtomic(local, raw); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}

func sameFile(a, b string) bool {
	x, errA := os.ReadFile(a)
	y, errB := os.ReadFile(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".profile-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

func publishedVersions(dir, name string) []string {
	entries, _ := os.ReadDir(filepath.Join(dir, name))
	var versions []string
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(dir, name, e.Name(), "profile.json")); err == nil && versionFolder.MatchString(e.Name()) {
			versions = append(versions, e.Name())
		}
	}
	slices.SortFunc(versions, func(a, b string) int {
		switch {
		case versionLess(a, b):
			return -1
		case versionLess(b, a):
			return 1
		}
		return cmp.Compare(a, b)
	})
	return versions
}

// The folder lbf loads a --profile from: the argument itself, or the cached copy of a published profile.
func profileDir(ctx context.Context, o options, arg string, offline bool) (string, error) {
	name, version, ok := registryRef(arg)
	if !ok {
		return arg, nil
	}
	if name == "bronze" {
		return "", nil
	}
	if offline && version != "" {
		account := cmp.Or(o.account, "*")
		root, _ := cacheRoot()
		cached, _ := filepath.Glob(filepath.Join(root, "lbf", "profiles", account, name, version, "profile.json"))
		if len(cached) > 0 && !slices.ContainsFunc(cached[1:], func(p string) bool { return !sameFile(p, cached[0]) }) {
			return filepath.Dir(cached[0]), nil
		}
	}
	t, err := resolveTarget(ctx, o.sasEnv, "download", o.tenant, o.tag, o.account, profilesContainer)
	if err != nil && o.sasEnv != "" {
		return "", fmt.Errorf("--profile %s is read from the storage account's %s container, which this --sas-env file does not cover; give the profile as a folder instead, from 'lbf profiles pull %s': %w", arg, profilesContainer, arg, err)
	}
	if err != nil {
		return "", err
	}
	return publishedProfile(ctx, t, name, version)
}

// The cached folder of a published profile, the latest version unless one is given.
func publishedProfile(ctx context.Context, t target, name, version string) (string, error) {
	if cache, err := profilesCache(t.Account); err == nil && version != "" {
		if _, err := os.Stat(filepath.Join(cache, name, version, "profile.json")); err == nil {
			return filepath.Join(cache, name, version), nil
		}
	}
	dir, err := syncProfiles(ctx, t)
	if err != nil {
		return "", err
	}
	versions := publishedVersions(dir, name)
	if len(versions) == 0 {
		return "", fmt.Errorf("there is no folder %s here, and no profile %s is published in %s; 'lbf profiles' lists those that are", name, name, t.Account)
	}
	if version == "" {
		version = versions[len(versions)-1]
		logf("[profile] using %s@%s, the latest; give --profile %s@%s to keep to it\n", name, version, name, version)
	} else if !slices.Contains(versions, version) {
		return "", fmt.Errorf("%s has no version %s in %s; published: %s", name, version, t.Account, strings.Join(versions, ", "))
	}
	return filepath.Join(dir, name, version), nil
}

func profiles(ctx context.Context, o options, args []string) error {
	sub, arg := "", ""
	if len(args) > 0 {
		sub = args[0]
	}
	if len(args) > 1 {
		arg = args[1]
	}
	switch {
	case sub == "" || sub == "list":
		return listProfiles(ctx, o)
	case sub == "show" && arg != "":
		dir, err := profileDir(ctx, o, arg, false)
		if err != nil {
			return err
		}
		prof, err := loadProfile(dir)
		if err != nil {
			return err
		}
		fmt.Print(describeProfile(prof, arg))
		return nil
	case sub == "pull" && arg != "":
		return pullProfile(ctx, o, arg)
	case sub == "publish" && arg != "":
		return publishProfile(ctx, o, arg)
	}
	return errors.New("want 'lbf profiles', 'lbf profiles show NAME[@VERSION]', 'lbf profiles pull NAME[@VERSION]' or 'lbf profiles publish DIR'")
}

func listProfiles(ctx context.Context, o options) error {
	t, err := resolveTarget(ctx, o.sasEnv, "download", o.tenant, o.tag, o.account, profilesContainer)
	if err != nil {
		return err
	}
	dir, err := syncProfiles(ctx, t)
	if err != nil {
		return err
	}
	entries, _ := os.ReadDir(dir)
	shown := 0
	for _, e := range entries {
		versions := publishedVersions(dir, e.Name())
		if len(versions) == 0 {
			continue
		}
		latest := versions[len(versions)-1]
		var head struct{ Title, Description string }
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name(), latest, "profile.json"))
		_ = json.Unmarshal(raw, &head)
		fmt.Printf("%s@%s  %s\n", e.Name(), latest, cmp.Or(head.Title, e.Name()))
		if head.Description != "" {
			fmt.Printf("    %s\n", head.Description)
		}
		if len(versions) > 1 {
			older := slices.Clone(versions[:len(versions)-1])
			slices.Reverse(older)
			fmt.Printf("    also %s\n", strings.Join(older, ", "))
		}
		shown++
	}
	if shown == 0 {
		fmt.Printf("No profiles are published in %s yet; 'lbf profiles publish DIR' publishes one\n", t.Account)
	}
	return nil
}

// What a profile asks of a publisher, in the flags that give it.
func describeProfile(prof *profile, ref string) string {
	var b strings.Builder
	target := prof.rules[len(prof.rules)-1]
	var head struct{ Title, Description string }
	_ = json.Unmarshal(target.raw, &head)
	fmt.Fprintf(&b, "%s  %s\n", target.id, cmp.Or(head.Title, target.id))
	if head.Description != "" {
		fmt.Fprintf(&b, "  %s\n", head.Description)
	}
	if len(prof.rules) > 1 {
		var chain []string
		for _, r := range prof.rules[:len(prof.rules)-1] {
			chain = append(chain, r.id)
		}
		fmt.Fprintf(&b, "  Builds on %s\n", strings.Join(chain, ", "))
	}

	type ask struct{ flag, need, hint string }
	var asks []ask
	seen := map[string]bool{}
	add := func(a ask) {
		if !seen[a.flag] {
			seen[a.flag] = true
			asks = append(asks, a)
		}
	}
	type schema struct {
		Required   []string
		Properties map[string]json.RawMessage
		AllOf      []json.RawMessage `json:"allOf"`
	}
	// A schema and those it combines with allOf, which profiles use to group rules.
	var parts func(raw json.RawMessage) []schema
	parts = func(raw json.RawMessage) []schema {
		var s schema
		_ = json.Unmarshal(raw, &s)
		out := []schema{s}
		for _, sub := range s.AllOf {
			out = append(out, parts(sub)...)
		}
		return out
	}
	derived := false
	for _, r := range slices.Backward(prof.rules) {
		for _, s := range parts(r.raw) {
			if slices.Contains(s.Required, "isBasedOn") || slices.Contains(s.Required, "mentions") {
				derived = true
			}
			for _, props := range parts(s.Properties["additionalProperty"]) {
				for _, n := range slices.Concat(props.Required, slices.Sorted(maps.Keys(props.Properties))) {
					need := "optional"
					if slices.Contains(props.Required, n) {
						need = "required"
					}
					add(ask{"--property " + n + "=...", need, annotations(props.Properties[n])})
				}
			}
			if h := annotations(s.Properties["hasPart"]); h != "" {
				add(ask{"files", "required", h})
			}
		}
	}
	if derived {
		add(ask{"--derived-from ID --instrument ...", "required", "what it was made from and with"})
	}
	for _, f := range []string{"name", "description"} {
		add(ask{"--" + f + " ...", "optional", "lbf fills it in if left out"})
	}
	b.WriteString("\n  Asks for:\n")
	width := 0
	for _, a := range asks {
		width = max(width, len([]rune(a.flag)))
	}
	for _, a := range asks {
		fmt.Fprintf(&b, "    %-*s  %-8s  %s\n", width, a.flag, a.need, a.hint)
	}
	fmt.Fprintf(&b, "\n  Publish with:\n    lbf publish <folder> --profile %s", ref)
	for _, a := range asks {
		if a.need == "required" && strings.HasPrefix(a.flag, "--") {
			b.WriteString(" " + a.flag)
		}
	}
	b.WriteString("\n")
	return b.String()
}

func annotations(raw json.RawMessage) string {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	var parts []string
	for _, k := range []string{"title", "description"} {
		if s, ok := m[k].(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	if ex, ok := m["examples"].([]any); ok && len(ex) > 0 {
		parts = append(parts, fmt.Sprintf("e.g. %v", ex[0]))
	}
	return strings.Join(parts, "; ")
}

// Writes a published profile and those it builds on as <out>/<name>/<version>/profile.json, for --profile on a machine that cannot sign in.
func pullProfile(ctx context.Context, o options, arg string) error {
	if _, _, ok := registryRef(arg); !ok {
		return fmt.Errorf("%q is not a published profile's NAME or NAME@VERSION", arg)
	}
	dir, err := profileDir(ctx, o, arg, false)
	if err != nil {
		return err
	}
	prof, err := loadProfile(dir)
	if err != nil {
		return err
	}
	target, err := writeProfiles(prof, o.out)
	if err != nil {
		return err
	}
	fmt.Println(target)
	return nil
}

// Writes each profile in the chain but bronze as <out>/<name>/<version>/profile.json and returns the chosen one's folder.
func writeProfiles(prof *profile, out string) (string, error) {
	var target string
	for _, r := range prof.rules[1:] {
		name, version, err := profilePath(r.id)
		if err != nil {
			return "", err
		}
		path := filepath.Join(out, name, version, "profile.json")
		if have, err := os.ReadFile(path); err == nil && !bytes.Equal(have, r.raw) {
			return "", fmt.Errorf("%s already holds another %s@%s; move it aside", path, name, version)
		}
		if err := writeAtomic(path, r.raw); err != nil {
			return "", err
		}
		target = filepath.Dir(path)
	}
	return target, nil
}

func publishProfile(ctx context.Context, o options, dir string) error {
	if _, err := loadProfile(dir); err != nil {
		return err
	}
	t, err := resolveTarget(ctx, o.sasEnv, "upload", o.tenant, o.tag, o.account, profilesContainer)
	if err != nil {
		return err
	}
	msg, err := publishProfileTo(ctx, t, dir)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

func publishProfileTo(ctx context.Context, t target, dir string) (string, error) {
	prof, err := loadProfile(dir)
	if err != nil {
		return "", err
	}
	if len(prof.rules) < 2 {
		return "", errors.New("lbf's bronze profile is compiled into lbf and is never published")
	}
	name, version, err := profilePath(prof.ID)
	if err != nil {
		return "", err
	}
	cache, err := syncProfiles(ctx, t)
	if err != nil {
		return "", err
	}
	for _, r := range prof.rules[1 : len(prof.rules)-1] {
		pname, pversion, err := profilePath(r.id)
		if err != nil {
			return "", err
		}
		published, err := os.ReadFile(filepath.Join(cache, pname, pversion, "profile.json"))
		if err != nil {
			return "", fmt.Errorf("%s builds on %s, which is not published in %s; publish it first", prof.ID, r.id, t.Account)
		}
		if !bytes.Equal(published, r.raw) {
			return "", fmt.Errorf("%s builds on %s, but the copy beside it differs from the one published in %s", prof.ID, r.id, t.Account)
		}
	}
	cc, err := t.client()
	if err != nil {
		return "", err
	}
	if err := checkWrite(ctx, cc, t); err != nil {
		return "", err
	}
	raw := prof.rules[len(prof.rules)-1].raw
	blobName := name + "/" + version + "/profile.json"
	_, err = cc.NewBlockBlobClient(blobName).UploadBuffer(ctx, raw, &blockblob.UploadBufferOptions{
		HTTPHeaders: &blob.HTTPHeaders{BlobContentType: to.Ptr("application/schema+json")},
		AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{
			IfNoneMatch: to.Ptr(azcore.ETagAny),
		}},
	})
	if bloberror.HasCode(err, bloberror.BlobAlreadyExists, bloberror.ConditionNotMet) {
		stored, derr := downloadBuffer(ctx, cc, blobName)
		if derr != nil {
			return "", derr
		}
		if !bytes.Equal(stored, raw) {
			return "", fmt.Errorf("%s@%s is already published in %s with other contents, and a published profile never changes; give this one a new version", name, version, t.Account)
		}
		return fmt.Sprintf("%s@%s is already published in %s, unchanged", name, version, t.Account), nil
	}
	if err != nil {
		return "", fmt.Errorf("publishing %s: %w", blobName, err)
	}
	if err := writeAtomic(filepath.Join(cache, name, version, "profile.json"), raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("Published %s@%s in %s\nUse it with: lbf publish <folder> --profile %s@%s", name, version, t.Account, name, version), nil
}
