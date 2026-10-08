package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"golang.org/x/mod/semver"
)

// Published profiles live in the storage account beside the datasets that meet them, as <name>/<version>/profile.json.
const profilesContainer = "profiles"

var (
	profileRef  = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*)(?:@(v?[0-9]+(?:\.[0-9]+){0,2}))?$`)
	profileSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// A published profile: its name, and its version unless the latest is meant.
type published struct{ name, version string }

func (p published) String() string { return p.name + "@" + p.version }

// Where it is kept, under the profiles container or a local copy of it.
func (p published) blob() string         { return p.name + "/" + p.version + "/profile.json" }
func (p published) in(dir string) string { return filepath.Join(dir, filepath.FromSlash(p.blob())) }

// The published profile a blob in the profiles container holds, if it holds one.
func publishedBlob(name string) (published, bool) {
	segs := strings.Split(name, "/")
	if len(segs) != 3 || segs[2] != "profile.json" || !profileSlug.MatchString(segs[0]) || !versionFolder.MatchString(segs[1]) {
		return published{}, false
	}
	return published{segs[0], segs[1]}, true
}

// A --profile names a published profile unless it is a path, or a profile.json, or a folder holding one, here.
func publishedRef(arg string) (published, bool) {
	if arg == "" || strings.ContainsAny(arg, `/\`) {
		return published{}, false
	}
	if info, err := os.Stat(arg); err == nil && !info.IsDir() {
		return published{}, false
	}
	if _, err := os.Stat(filepath.Join(arg, "profile.json")); err == nil {
		return published{}, false
	}
	m := profileRef.FindStringSubmatch(arg)
	if m == nil {
		return published{}, false
	}
	return published{m[1], m[2]}, true
}

// The name and version a profile is published under: the last two segments of its $id.
func publishedAs(id string) (published, error) {
	u, err := url.Parse(id)
	if err == nil {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) >= 2 && profileSlug.MatchString(segs[len(segs)-2]) && versionFolder.MatchString(segs[len(segs)-1]) {
			return published{segs[len(segs)-2], segs[len(segs)-1]}, nil
		}
	}
	return published{}, fmt.Errorf("$id %s does not end in /<name>/<version>, such as https://w3id.org/lbf/profiles/plate-read/0.1.0, so it cannot be published", id)
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
			ref, ok := publishedBlob(*b.Name)
			if !ok {
				continue
			}
			local := ref.in(dir)
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
		if _, err := os.Stat(published{name, e.Name()}.in(dir)); err == nil && versionFolder.MatchString(e.Name()) {
			versions = append(versions, e.Name())
		}
	}
	slices.SortFunc(versions, func(a, b string) int {
		return cmp.Or(semver.Compare("v"+strings.TrimPrefix(a, "v"), "v"+strings.TrimPrefix(b, "v")), cmp.Compare(a, b))
	})
	return versions
}

// The folder lbf loads a --profile from: the argument itself, or the cached copy of a published profile.
func profileDir(ctx context.Context, o options, arg string, offline bool) (string, error) {
	ref, ok := publishedRef(arg)
	if !ok {
		return arg, nil
	}
	if ref.name == "bronze" {
		return "", nil
	}
	if offline && ref.version != "" {
		cache, _ := profilesCache(cmp.Or(o.account, "*"))
		cached, _ := filepath.Glob(ref.in(cache))
		if len(cached) > 0 && !slices.ContainsFunc(cached[1:], func(p string) bool { return !sameFile(p, cached[0]) }) {
			return filepath.Dir(cached[0]), nil
		}
	}
	t, err := o.target(ctx, "download", profilesContainer)
	if err != nil && o.sasEnv != "" {
		return "", fmt.Errorf("--profile %s is read from the storage account's %s container, which this --sas-env file does not cover; give the profile as a folder instead, from 'lbf profiles pull %s': %w", arg, profilesContainer, arg, err)
	}
	if err != nil {
		return "", err
	}
	return publishedProfile(ctx, t, ref)
}

// The cached folder of a published profile, the latest version unless one is given.
func publishedProfile(ctx context.Context, t target, ref published) (string, error) {
	if cache, err := profilesCache(t.Account); err == nil && ref.version != "" {
		if _, err := os.Stat(ref.in(cache)); err == nil {
			return filepath.Dir(ref.in(cache)), nil
		}
	}
	dir, err := syncProfiles(ctx, t)
	if err != nil {
		return "", err
	}
	versions := publishedVersions(dir, ref.name)
	if len(versions) == 0 {
		return "", fmt.Errorf("there is no folder %s here, and no profile %s is published in %s; 'lbf profiles' lists those that are", ref.name, ref.name, t.Account)
	}
	if ref.version == "" {
		ref.version = versions[len(versions)-1]
		logf("[profile] using %s, the latest; give --profile %s to keep to it\n", ref, ref)
	} else if !slices.Contains(versions, ref.version) {
		return "", fmt.Errorf("%s has no version %s in %s; published: %s", ref.name, ref.version, t.Account, strings.Join(versions, ", "))
	}
	return filepath.Dir(ref.in(dir)), nil
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
	t, err := o.target(ctx, "download", profilesContainer)
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
		latest := published{e.Name(), versions[len(versions)-1]}
		var head struct{ Title, Description string }
		raw, _ := os.ReadFile(latest.in(dir))
		_ = json.Unmarshal(raw, &head)
		fmt.Printf("%s  %s\n", latest, cmp.Or(head.Title, e.Name()))
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

// What 'lbf profiles show' prints: the profile, what it builds on, and what it asks of a publisher.
func describeProfile(prof *profile, ref string) string {
	var b strings.Builder
	target := prof.chosen()
	fmt.Fprintf(&b, "%s  %s\n", target.id, cmp.Or(target.title, target.id))
	if target.schema.Description != "" {
		fmt.Fprintf(&b, "  %s\n", target.schema.Description)
	}
	if bases := slices.DeleteFunc(prof.ids(), func(id string) bool { return id == target.id }); len(bases) > 0 {
		fmt.Fprintf(&b, "  Builds on %s\n", strings.Join(bases, ", "))
	}
	asks := prof.asks()
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

// Writes a published profile and those it builds on as <out>/<name>/<version>/profile.json, for --profile on a machine that cannot sign in.
func pullProfile(ctx context.Context, o options, arg string) error {
	if _, ok := publishedRef(arg); !ok {
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
		ref, err := publishedAs(r.id)
		if err != nil {
			return "", err
		}
		path := ref.in(out)
		if have, err := os.ReadFile(path); err == nil && !bytes.Equal(have, r.raw) {
			return "", fmt.Errorf("%s already holds another %s; move it aside", path, ref)
		}
		if err := writeAtomic(path, r.raw); err != nil {
			return "", err
		}
		target = filepath.Dir(path)
	}
	return target, nil
}

func publishProfile(ctx context.Context, o options, dir string) error {
	prof, err := loadProfile(dir)
	if err != nil {
		return err
	}
	t, err := o.target(ctx, "upload", profilesContainer)
	if err != nil {
		return err
	}
	msg, err := publishProfileTo(ctx, t, prof)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

func publishProfileTo(ctx context.Context, t target, prof *profile) (string, error) {
	if len(prof.rules) < 2 {
		return "", errors.New("lbf's bronze profile is compiled into lbf and is never published")
	}
	ref, err := publishedAs(prof.ID)
	if err != nil {
		return "", err
	}
	cache, err := syncProfiles(ctx, t)
	if err != nil {
		return "", err
	}
	for _, r := range prof.rules[1 : len(prof.rules)-1] {
		base, err := publishedAs(r.id)
		if err != nil {
			return "", err
		}
		stored, err := os.ReadFile(base.in(cache))
		if err != nil {
			return "", fmt.Errorf("%s builds on %s, which is not published in %s; publish it first", prof.ID, r.id, t.Account)
		}
		if !bytes.Equal(stored, r.raw) {
			return "", fmt.Errorf("%s builds on %s, but the copy beside it differs from the one published in %s; 'lbf profiles pull %s' fetches the published one", prof.ID, r.id, t.Account, base)
		}
	}
	cc, err := t.client()
	if err != nil {
		return "", err
	}
	if err := checkWrite(ctx, cc, t); err != nil {
		return "", err
	}
	raw := prof.chosen().raw
	blobName := ref.blob()
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
			return "", fmt.Errorf("%s is already published in %s with other contents, and a published profile never changes; give this one a new version", ref, t.Account)
		}
		return fmt.Sprintf("%s is already published in %s, unchanged", ref, t.Account), nil
	}
	if err != nil {
		return "", fmt.Errorf("publishing %s: %w", blobName, err)
	}
	if err := writeAtomic(ref.in(cache), raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("Published %s in %s\nUse it with: lbf publish <folder> --profile %s", ref, t.Account, ref), nil
}
