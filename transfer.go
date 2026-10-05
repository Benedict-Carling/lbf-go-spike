package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"golang.org/x/sync/errgroup"
)

const (
	parallelFiles = 8
	blockSize     = 8 << 20
	blockWorkers  = 4
	maxBlocks     = 50000
	crateName     = "ro-crate-metadata.json"
)

type publication struct {
	ID         string
	Source     string
	Files      []localFile
	Provenance provenance
	Profile    *profile
}

// Everything that can fail without Azure happens here, so a bad dataset never reaches sign-in or upload.
func preparePublication(input string, provFlags provenanceFlags, profileDir string) (publication, error) {
	prov, err := provFlags.load()
	if err != nil {
		return publication{}, err
	}
	prof, err := loadProfile(profileDir)
	if err != nil {
		return publication{}, err
	}
	source, files, err := datasetFiles(input)
	if err != nil {
		return publication{}, err
	}
	if len(files) == 0 {
		return publication{}, fmt.Errorf("%s contains no files to upload", input)
	}
	return publication{ID: newID(), Source: source, Files: files, Provenance: prov, Profile: prof}, nil
}

// Checked here rather than in preparePublication because the uploader is only known once signed in.
func (p publication) validate(t target) (time.Time, []string, error) {
	published := time.Now()
	ids := p.Profile.ids()
	conformsTo := ids
	if p.Provenance.DerivedFrom != "" {
		conformsTo = append([]string{processRunCrate}, ids...)
	}
	if err := p.Profile.validate(p.view(t, published, conformsTo)); err != nil {
		return time.Time{}, nil, err
	}
	logf("[profile] meets %s\n", strings.Join(ids, ", "))
	return published, conformsTo, nil
}

func (p publication) crate(t target) ([]byte, error) {
	published, conformsTo, err := p.validate(t)
	if err != nil {
		return nil, err
	}
	return buildCrate(p.ID, p.Source, p.Files, t, p.Provenance, conformsTo, published)
}

func (p publication) view(t target, published time.Time, conformsTo []string) map[string]any {
	name := filepath.Base(p.Source)
	files := make([]map[string]any, len(p.Files))
	for i, f := range p.Files {
		files[i] = map[string]any{"path": strings.TrimPrefix(f.Rel, name+"/"), "size": f.Size}
	}
	crate := map[string]any{
		"identifier":         p.ID,
		"datePublished":      timestamp(published),
		"conformsTo":         conformsTo,
		"additionalProperty": p.Provenance.Properties,
	}
	if user := strings.TrimSpace(t.User); user != "" {
		crate["creator"] = user
	}
	if p.Provenance.DerivedFrom != "" {
		crate["wasDerivedFrom"] = p.Provenance.DerivedFrom
		crate["instrument"] = p.Provenance.Instruments
	}
	return map[string]any{
		"data":  map[string]any{"name": name, "files": files},
		"crate": crate,
	}
}

func upload(ctx context.Context, t target, pub publication) error {
	published, conformsTo, err := pub.validate(t)
	if err != nil {
		return err
	}
	cc, err := t.client()
	if err != nil {
		return err
	}

	id := pub.ID
	status("Checking write access to " + t.Account + "/" + t.Container)
	err = checkWrite(ctx, cc, t, id)
	status("")
	if err != nil {
		return err
	}
	var total int64
	for _, f := range pub.Files {
		total += f.Size
	}
	logf("Uploading %d files (%s) from %s as %s\n", len(pub.Files), humanBytes(total), pub.Source, id)

	started := time.Now()
	jobs := make([]job, len(pub.Files))
	for i := range pub.Files {
		f := &pub.Files[i]
		jobs[i] = job{name: f.Rel, size: f.Size, run: func(ctx context.Context, progress func(int64)) error {
			return uploadFile(ctx, cc, id+"/"+f.Rel, f, progress)
		}}
	}
	err = transferAll(ctx, newProgress(os.Stderr, len(jobs), total), jobs)
	if err == nil {
		err = uploadCrate(ctx, cc, t, pub, published, conformsTo)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("interrupted; %s was left without a crate, so fetch will refuse it. Publish again to get a new ID", id)
	}
	if err != nil {
		return fmt.Errorf("%w\n%s was left without a crate, so fetch will refuse it. Publish again to get a new ID", err, id)
	}
	logDone(started, total)
	return nil
}

// Hashes the bytes as they are sent, and refuses a file that changed meanwhile, so the crate records exactly what landed.
func uploadFile(ctx context.Context, cc *container.Client, name string, f *localFile, progress func(int64)) error {
	fh, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer fh.Close()
	h := sha256.New()
	r := &countingReader{r: io.TeeReader(io.LimitReader(fh, f.Size), h), progress: progress}
	_, err = cc.NewBlockBlobClient(name).UploadStream(ctx, r, &blockblob.UploadStreamOptions{
		BlockSize:   max(blockSize, (f.Size+maxBlocks-1)/maxBlocks),
		Concurrency: blockWorkers,
		AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{
			IfNoneMatch: to.Ptr(azcore.ETagAny),
		}},
	})
	if bloberror.HasCode(err, bloberror.BlobAlreadyExists, bloberror.ConditionNotMet) {
		return fmt.Errorf("%s already exists in the container; refusing to overwrite", name)
	}
	if err != nil {
		return fmt.Errorf("uploading %s: %w", f.Rel, err)
	}
	after, err := os.Stat(f.Path)
	if err != nil || r.n != f.Size || after.Size() != f.Size || !after.ModTime().Equal(f.ModTime) {
		return fmt.Errorf("%s changed while it was being uploaded; wait until whatever writes it has finished, then publish again", f.Rel)
	}
	f.SHA256 = hex.EncodeToString(h.Sum(nil))
	return nil
}

func uploadCrate(ctx context.Context, cc *container.Client, t target, pub publication, published time.Time, conformsTo []string) error {
	crate, err := buildCrate(pub.ID, pub.Source, pub.Files, t, pub.Provenance, conformsTo, published)
	if err != nil {
		return err
	}
	_, err = cc.NewBlockBlobClient(pub.ID+"/"+crateName).UploadBuffer(ctx, crate, &blockblob.UploadBufferOptions{
		HTTPHeaders: &blob.HTTPHeaders{BlobContentType: new("application/json")},
		AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{
			IfNoneMatch: to.Ptr(azcore.ETagAny),
		}},
	})
	if bloberror.HasCode(err, bloberror.BlobAlreadyExists, bloberror.ConditionNotMet) {
		return fmt.Errorf("%s/%s already exists in the container; refusing to overwrite", pub.ID, crateName)
	}
	if err != nil {
		return fmt.Errorf("uploading %s: %w", crateName, err)
	}
	return nil
}

type countingReader struct {
	r        io.Reader
	n        int64
	progress func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	c.progress(c.n)
	return n, err
}

type fetched struct {
	path, dataPath string
}

func download(ctx context.Context, t target, id, outDir string) (fetched, error) {
	cc, err := t.client()
	if err != nil {
		return fetched{}, err
	}

	prefix := id + "/"
	status("Listing " + id)
	defer status("")
	stored := map[string]int64{}
	pager := cc.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Prefix:  new(prefix),
		Include: container.ListBlobsInclude{Metadata: true},
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fetched{}, fmt.Errorf("listing %s: %w", id, err)
		}
		for _, b := range page.Segment.BlobItems {
			if !isDirectoryMarker(b) {
				stored[strings.TrimPrefix(*b.Name, prefix)] = deref(b.Properties.ContentLength)
			}
		}
	}
	if len(stored) == 0 {
		return fetched{}, fmt.Errorf("no dataset %s in %s", id, t.containerURL())
	}
	if _, ok := stored[crateName]; !ok {
		return fetched{}, fmt.Errorf("dataset %s has no %s, so its upload never finished; nothing was downloaded", id, crateName)
	}

	status("Reading the crate of " + id)
	crate, err := downloadBuffer(ctx, cc, prefix+crateName)
	if err != nil {
		return fetched{}, fmt.Errorf("downloading the crate of %s: %w", id, err)
	}
	status("")
	files, err := crateFiles(crate)
	if err != nil {
		return fetched{}, fmt.Errorf("dataset %s: %w", id, err)
	}
	if problems := compareStored(files, stored); len(problems) > 0 {
		return fetched{}, problemList(fmt.Sprintf("dataset %s does not match its crate, so nothing was downloaded:", id), problems)
	}
	if problems := localNameProblems(files, runtime.GOOS); len(problems) > 0 {
		return fetched{}, problemList(fmt.Sprintf("dataset %s cannot be saved on this computer, so nothing was downloaded:", id), problems)
	}

	root, err := filepath.Abs(filepath.Join(outDir, id))
	if err != nil {
		return fetched{}, err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return fetched{}, err
	}
	unlock, err := lockPath(ctx, root+".lock", func() {
		logf("Waiting for another fetch of %s into %s to finish\n", id, filepath.Dir(root))
	})
	if err != nil {
		if ctx.Err() != nil {
			return fetched{}, fmt.Errorf("interrupted while waiting for another fetch of %s", id)
		}
		return fetched{}, err
	}
	defer unlock()
	if _, err := os.Lstat(root); err == nil {
		status("Checking the existing " + root)
		problem, err := verifyDir(root, files, crate)
		if err != nil {
			return fetched{}, err
		}
		if problem != "" {
			return fetched{}, fmt.Errorf("%s already exists but is not dataset %s (%s); move it aside or fetch with another --out", root, id, problem)
		}
		logf("%s is already here and matches its crate\n", root)
		return fetched{root, dataDir(root, files)}, nil
	}
	partial := root + ".partial"
	if _, err := os.Lstat(partial); err == nil {
		logf("Discarding %s, left by an earlier fetch that did not finish\n", partial)
		if err := os.RemoveAll(partial); err != nil {
			return fetched{}, err
		}
	}

	var total int64
	hashed := 0
	for _, f := range files {
		total += f.Size
		if f.SHA256 != "" {
			hashed++
		}
	}
	logf("Downloading %d files (%s) to %s\n", len(files), humanBytes(total), root)
	if hashed < len(files) {
		logf("[verify] %d of %d files have no checksum in the crate (published before lbf recorded them); only their sizes are checked\n", len(files)-hashed, len(files))
	}

	if err := os.MkdirAll(partial, 0o755); err != nil {
		return fetched{}, err
	}
	dir, err := os.OpenRoot(partial)
	if err != nil {
		return fetched{}, err
	}
	started := time.Now()
	jobs := make([]job, len(files))
	for i, f := range files {
		jobs[i] = job{name: f.Rel, size: f.Size, run: func(ctx context.Context, progress func(int64)) error {
			return downloadFile(ctx, cc, dir, prefix+f.Rel, f, progress)
		}}
	}
	err = transferAll(ctx, newProgress(os.Stderr, len(jobs), total), jobs)
	if err == nil {
		err = dir.WriteFile(crateName, crate, 0o644)
	}
	dir.Close()
	if ctx.Err() != nil {
		return fetched{}, fmt.Errorf("interrupted; nothing was saved as %s, and the next fetch starts again", root)
	}
	if err != nil {
		return fetched{}, err
	}
	if err := os.Rename(partial, root); err != nil {
		return fetched{}, err
	}
	logDone(started, total)
	return fetched{root, dataDir(root, files)}, nil
}

// The folder that was published, which every file sits under; root itself if they do not share one.
func dataDir(root string, files []crateFile) string {
	top := ""
	for _, f := range files {
		first, _, nested := strings.Cut(f.Rel, "/")
		if !nested || (top != "" && first != top) {
			return root
		}
		top = first
	}
	if top == "" {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(top))
}

func downloadFile(ctx context.Context, cc *container.Client, dir *os.Root, name string, f crateFile, progress func(int64)) error {
	fh, err := createIn(dir, f.Rel)
	if err != nil {
		return err
	}
	_, err = cc.NewBlobClient(name).DownloadFile(ctx, fh, &blob.DownloadFileOptions{
		BlockSize:   blockSize,
		Concurrency: blockWorkers,
		Progress:    progress,
	})
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading %s: %w", name, err)
	}
	problem, err := checkFile(dir, f)
	if err != nil {
		return err
	}
	if problem != "" {
		return fmt.Errorf("%s after download; fetch again, and if it recurs the stored copy differs from what was published", problem)
	}
	return nil
}

func downloadBuffer(ctx context.Context, cc *container.Client, name string) ([]byte, error) {
	resp, err := cc.NewBlobClient(name).DownloadStream(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

type crateFile struct {
	Rel    string
	Size   int64
	SHA256 string
}

// The crate, written last, is the record of what landed; fetch trusts it over the listing.
func crateFiles(crate []byte) ([]crateFile, error) {
	var doc struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(crate, &doc); err != nil {
		return nil, fmt.Errorf("its %s is not valid JSON: %w", crateName, err)
	}
	entities := map[string]map[string]any{}
	for _, e := range doc.Graph {
		if id, ok := e["@id"].(string); ok {
			entities[id] = e
		}
	}
	var parts []any
	switch hp := entities["./"]["hasPart"].(type) {
	case []any:
		parts = hp
	case map[string]any:
		parts = []any{hp}
	}
	files := make([]crateFile, 0, len(parts))
	for _, p := range parts {
		ref, _ := p.(map[string]any)
		id, _ := ref["@id"].(string)
		rel, err := url.PathUnescape(id)
		if err != nil || rel == "" {
			return nil, fmt.Errorf("its %s lists an unreadable file %q", crateName, id)
		}
		size, err := strconv.ParseInt(fmt.Sprint(entities[id]["contentSize"]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("its %s gives no size for %s", crateName, rel)
		}
		sum, _ := entities[id]["sha256"].(string)
		files = append(files, crateFile{Rel: rel, Size: size, SHA256: sum})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("its %s lists no files", crateName)
	}
	return files, nil
}

func compareStored(files []crateFile, stored map[string]int64) []string {
	var problems []string
	listed := map[string]bool{crateName: true}
	for _, f := range files {
		listed[f.Rel] = true
		size, ok := stored[f.Rel]
		switch {
		case !ok:
			problems = append(problems, f.Rel+": listed in the crate but not stored")
		case size != f.Size:
			problems = append(problems, fmt.Sprintf("%s: stored as %d bytes, but the crate records %d", f.Rel, size, f.Size))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(stored)) {
		if !listed[name] {
			problems = append(problems, name+": stored but not listed in the crate")
		}
	}
	return problems
}

func localNameProblems(files []crateFile, goos string) []string {
	var problems []string
	seen := map[string]string{}
	for _, f := range files {
		if goos == "windows" {
			if p := nameProblem(f.Rel); p != "" {
				problems = append(problems, f.Rel+": "+p)
			}
		}
		if goos == "windows" || goos == "darwin" {
			folded := strings.ToLower(f.Rel)
			if other, ok := seen[folded]; ok {
				problems = append(problems, fmt.Sprintf("%s and %s differ only in case, so one would overwrite the other here", other, f.Rel))
			}
			seen[folded] = f.Rel
		}
	}
	return problems
}

// Returns why root does not hold exactly these files and crate, or "" if it does.
func verifyDir(root string, files []crateFile, crate []byte) (string, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	want := map[string]bool{crateName: true}
	for _, f := range files {
		want[f.Rel] = true
		if problem, err := checkFile(dir, f); problem != "" || err != nil {
			return problem, err
		}
	}
	local, err := dir.ReadFile(crateName)
	if err != nil || !bytes.Equal(local, crate) {
		return crateName + " differs", nil
	}
	extra := ""
	err = fs.WalkDir(dir.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !want[path] {
			extra = path + " is not part of the dataset"
			return fs.SkipAll
		}
		return nil
	})
	return extra, err
}

// Returns why the local copy of f is wrong, or "" if it matches the crate.
func checkFile(dir *os.Root, f crateFile) (string, error) {
	fh, err := dir.Open(filepath.FromSlash(f.Rel))
	if errors.Is(err, fs.ErrNotExist) {
		return f.Rel + " is missing", nil
	}
	if err != nil {
		return "", err
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() != f.Size {
		return fmt.Sprintf("%s is %d bytes, not %d", f.Rel, info.Size(), f.Size), nil
	}
	if f.SHA256 == "" {
		return "", nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return f.Rel + " has the wrong sha256", nil
	}
	return "", nil
}

type job struct {
	name string
	size int64
	run  func(ctx context.Context, progress func(int64)) error
}

func transferAll(ctx context.Context, p *progress, jobs []job) error {
	defer p.close()
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelFiles)
	for _, j := range jobs {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			b := p.start(j.name, j.size)
			err := j.run(gctx, b.set)
			p.end(b, err == nil)
			return err
		})
	}
	return g.Wait()
}

func logDone(started time.Time, total int64) {
	d := time.Since(started)
	logf("Done in %s (%s/s)\n", d.Round(time.Millisecond), humanBytes(rate(total, d)))
}

// An uncommitted block proves write access without creating a visible blob; the crate upload discards it.
func checkWrite(ctx context.Context, cc *container.Client, t target, id string) error {
	blockID := base64.StdEncoding.EncodeToString([]byte("lbf-write-check"))
	_, err := cc.NewBlockBlobClient(id+"/"+crateName).StageBlock(ctx, blockID, streaming.NopCloser(bytes.NewReader([]byte{0})), nil)
	if bloberror.HasCode(err, bloberror.AuthorizationPermissionMismatch, bloberror.AuthorizationFailure, bloberror.InsufficientAccountPermissions) {
		return fmt.Errorf("you cannot write to %s/%s, so nothing was uploaded; your login needs 'Storage Blob Data Contributor' there", t.Account, t.Container)
	}
	if err != nil {
		return fmt.Errorf("checking write access to %s/%s: %w", t.Account, t.Container, err)
	}
	return nil
}

// Hierarchical-namespace accounts list directories as zero-length blobs.
func isDirectoryMarker(b *container.BlobItem) bool {
	for k, v := range b.Metadata {
		if strings.EqualFold(k, "hdi_isfolder") && v != nil && *v == "true" {
			return true
		}
	}
	return false
}

// os.Root refuses names, including via symlinks, that would land outside the download directory.
func createIn(dir *os.Root, rel string) (*os.File, error) {
	name := filepath.FromSlash(rel)
	if err := dir.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return nil, fmt.Errorf("blob %s: %w", rel, err)
	}
	fh, err := dir.Create(name)
	if err != nil {
		return nil, fmt.Errorf("blob %s: %w", rel, err)
	}
	return fh, nil
}

func rate(n int64, d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(float64(n) / d.Seconds())
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
