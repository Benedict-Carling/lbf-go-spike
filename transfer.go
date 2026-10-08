package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

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
func preparePublication(input string, provFlags provenanceFlags, profileDir, id string) (publication, error) {
	if id == "" {
		id = newID()
	} else if !mintedID.MatchString(id) {
		return publication{}, fmt.Errorf("--id %q is not an ID from 'lbf new-id'", id)
	}
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
	if problems := blobNameProblems(id, files); len(problems) > 0 {
		return publication{}, problemList(fmt.Sprintf("%s cannot be published as it is, so nothing was uploaded:", input), problems)
	}
	return publication{ID: id, Source: source, Files: files, Provenance: prov, Profile: prof}, nil
}

func upload(ctx context.Context, t target, pub publication) error {
	published, err := pub.validate(t)
	if err != nil {
		return err
	}
	b, err := t.blobs(pub.ID)
	if err != nil {
		return err
	}

	id := pub.ID
	status("Checking write access to " + t.Account + "/" + t.Container)
	err = b.checkWrite(ctx)
	var stored map[string]crateFile
	if err == nil {
		stored, err = b.list(ctx)
	}
	status("")
	if err != nil {
		return err
	}
	if _, ok := stored[crateName]; ok {
		return confirmPublished(ctx, b, pub, stored)
	}

	if len(stored) > 0 {
		logf("Resuming %s: an earlier attempt stored %d of its %d files\n", id, len(stored), len(pub.Files))
	}
	toSend, problems, err := compareRecorded(ctx, pub.Files, stored)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return taken(fmt.Sprintf("an earlier attempt at %s stored different files, and lbf never changes a stored file, so nothing was uploaded", id), problems...)
	}

	var total int64
	jobs := make([]job, len(toSend))
	for i, n := range toSend {
		f := &pub.Files[n]
		total += f.Size
		jobs[i] = job{name: f.Rel, size: f.Size, run: func(ctx context.Context, progress func(int64)) error {
			return b.putFile(ctx, f, progress)
		}}
	}
	logf("Uploading %d files (%s) from %s as %s\n", len(jobs), humanBytes(total), pub.Source, id)

	started := time.Now()
	var crate []byte
	err = transferAll(ctx, newProgress(os.Stderr, len(jobs), total), jobs)
	if err == nil {
		crate, err = pub.buildCrate(t, published)
	}
	if err == nil {
		err = b.putCrate(ctx, crate)
	}
	if ctx.Err() != nil {
		return interrupted(context.WithoutCancel(ctx), b)
	}
	if errors.Is(err, errAnotherCrate) {
		return fmt.Errorf("%s: %w. Run the same publish with --id %s to check it records these files", id, err, id)
	}
	if _, ok := errors.AsType[idTaken](err); ok {
		return idTaken{fmt.Errorf("%w\n%s was left without a crate, so fetch will refuse it. Publish without --id, or with a new ID from 'lbf new-id'", err, id)}
	}
	if err != nil {
		return fmt.Errorf("%w\n%s was left without a crate, so fetch will refuse it. Run the same publish with --id %s to finish it", err, id, id)
	}
	if err := verifyLanded(ctx, b, crate); err != nil {
		return err
	}
	logDone(started, total)
	return nil
}

// The crate can land even though its response never arrives.
func interrupted(ctx context.Context, b blobs) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	landed, err := b.has(ctx, crateName)
	switch {
	case err != nil:
		return fmt.Errorf("interrupted before lbf could tell whether %s was published; run the same publish with --id %s to finish or check it", b.id, b.id)
	case landed:
		return fmt.Errorf("interrupted, but %s was published; run the same publish with --id %s to check what landed", b.id, b.id)
	default:
		return fmt.Errorf("interrupted; %s was left without a crate, so fetch will refuse it. Run the same publish with --id %s to finish it", b.id, b.id)
	}
}

// The ID holds other files or another crate, so retrying cannot help; lbf exits with exitIDTaken.
type idTaken struct{ error }

func (e idTaken) Unwrap() error { return e.error }

func taken(heading string, problems ...string) error {
	heading += "; publish without --id, or with a new ID from 'lbf new-id'"
	if len(problems) > 0 {
		heading += ":"
	}
	return idTaken{problemList(heading, problems)}
}

// A rerun of a publish that already finished succeeds only if it would have published the same thing.
func confirmPublished(ctx context.Context, b blobs, pub publication, stored map[string]crateFile) error {
	id := pub.ID
	status("Reading the crate of " + id)
	raw, err := b.read(ctx, crateName)
	status("")
	if err != nil {
		return fmt.Errorf("reading the crate of %s: %w", id, err)
	}
	crate, err := readCrate(raw)
	if err != nil {
		return fmt.Errorf("dataset %s: %w", id, err)
	}
	if problems := crate.differences(stored, true); len(problems) > 0 {
		return taken(fmt.Sprintf("%s is published, but what is stored differs from its crate, so fetch will refuse it", id), problems...)
	}
	stated, conformsTo, err := crate.statement()
	if err != nil {
		return taken(fmt.Sprintf("%s is already published, but lbf cannot read what its crate states (%v), so this publish cannot be checked against it", id, err))
	}

	// A name or description lbf wrote follows the folder, so it only has to match when it was given.
	if pub.Provenance.Name == "" {
		stated.Name = ""
	}
	if pub.Provenance.Description == "" {
		stated.Description = ""
	}
	var problems []string
	if !stated.sameAs(pub.Provenance) {
		problems = append(problems, "its name, description, parent, instruments or properties differ from these")
	}
	if !slices.Equal(slices.Sorted(slices.Values(conformsTo)), slices.Sorted(slices.Values(pub.conformsTo()))) {
		problems = append(problems, "it was checked against other profiles")
	}
	toSend, differ, err := compareRecorded(ctx, pub.Files, crate.byPath())
	if err != nil {
		return err
	}
	for _, n := range toSend {
		problems = append(problems, pub.Files[n].Rel+": not in it")
	}
	problems = append(problems, differ...)
	if len(problems) > 0 {
		return taken(fmt.Sprintf("%s is already published, differently, and a dataset never changes", id), problems...)
	}
	logf("%s is already published with exactly these files; nothing was uploaded\n", id)
	return nil
}

// Hashes each local file that is already recorded and returns every difference; the indexes of the rest are left to send.
func compareRecorded(ctx context.Context, files []localFile, recorded map[string]crateFile) ([]int, []string, error) {
	var toSend []int
	var problems []string
	var mu sync.Mutex
	var jobs []job
	var total int64
	local := map[string]bool{}
	for i := range files {
		f := &files[i]
		local[f.Rel] = true
		r, ok := recorded[f.Rel]
		switch {
		case !ok:
			toSend = append(toSend, i)
		case r.Size != f.Size:
			problems = append(problems, fmt.Sprintf("%s: %d bytes here, %d stored", f.Rel, f.Size, r.Size))
		case r.SHA256 == "":
			problems = append(problems, f.Rel+": stored without a sha256 (published before lbf recorded them), so it cannot be confirmed to match")
		default:
			total += f.Size
			jobs = append(jobs, job{name: f.Rel, size: f.Size, run: func(ctx context.Context, progress func(int64)) error {
				sum, err := hashFile(f, progress)
				if err != nil {
					return err
				}
				if sum != r.SHA256 {
					mu.Lock()
					problems = append(problems, f.Rel+": its content differs from what is stored")
					mu.Unlock()
					return nil
				}
				f.SHA256 = sum
				return nil
			}})
		}
	}
	for _, rel := range slices.Sorted(maps.Keys(recorded)) {
		if !local[rel] && rel != crateName {
			problems = append(problems, rel+": stored, but not here")
		}
	}
	if len(jobs) > 0 {
		logf("Checking %d files (%s) against what is stored\n", len(jobs), humanBytes(total))
		if err := transferAll(ctx, newProgress(os.Stderr, len(jobs), total), jobs); err != nil {
			return nil, nil, err
		}
	}
	slices.Sort(problems)
	return toSend, problems, nil
}

func hashFile(f *localFile, progress func(int64)) (string, error) {
	fh, err := os.Open(f.Path)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := sha256.New()
	r := &countingReader{r: io.LimitReader(fh, f.Size), progress: progress}
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	if err := unchanged(f, r.n); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func unchanged(f *localFile, read int64) error {
	after, err := os.Stat(f.Path)
	if err != nil || read != f.Size || after.Size() != f.Size || !after.ModTime().Equal(f.ModTime) {
		return fmt.Errorf("%s changed while it was being uploaded; wait until whatever writes it has finished, then publish again", f.Rel)
	}
	return nil
}

// Another publish of the same ID could have added files before this one's crate landed.
func verifyLanded(ctx context.Context, b blobs, raw []byte) error {
	crate, err := readCrate(raw)
	if err != nil {
		return err
	}
	stored, err := b.list(ctx)
	if err != nil {
		return err
	}
	if problems := crate.differences(stored, true); len(problems) > 0 {
		return taken(fmt.Sprintf("%s was published, but something else changed its files meanwhile, so fetch will refuse it", b.id), problems...)
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
	stated         *provenance
}

// What the crate states, for --json; a crate lbf cannot read is still fetched, without it.
func statedBy(id string, crate storedCrate) *provenance {
	p, _, err := crate.statement()
	if err != nil {
		logf("[fetch] cannot read what the crate of %s states: %v\n", id, err)
		return nil
	}
	return &p
}

var fetchHook = func(ctx context.Context, stage string) {}

func download(ctx context.Context, t target, id, outDir string) (fetched, error) {
	b, err := t.blobs(id)
	if err != nil {
		return fetched{}, err
	}

	status("Listing " + id)
	defer status("")
	stored, err := b.list(ctx)
	if err != nil {
		return fetched{}, err
	}
	if len(stored) == 0 {
		return fetched{}, fmt.Errorf("no dataset %s in %s", id, t.containerURL())
	}
	if _, ok := stored[crateName]; !ok {
		return fetched{}, fmt.Errorf("dataset %s has no %s, so its upload never finished; nothing was downloaded. Whoever published it can finish it by running the same publish with --id %s", id, crateName, id)
	}

	status("Reading the crate of " + id)
	raw, err := b.read(ctx, crateName)
	if err != nil {
		return fetched{}, fmt.Errorf("downloading the crate of %s: %w", id, err)
	}
	status("")
	crate, err := readCrate(raw)
	if err != nil {
		return fetched{}, fmt.Errorf("dataset %s: %w", id, err)
	}
	files := crate.Files
	if problems := crate.differences(stored, false); len(problems) > 0 {
		return fetched{}, problemList(fmt.Sprintf("dataset %s does not match its crate, so nothing was downloaded:", id), problems)
	}
	if problems := localNameProblems(files, runtime.GOOS); len(problems) > 0 {
		return fetched{}, problemList(fmt.Sprintf("dataset %s cannot be saved on this computer, so nothing was downloaded:", id), problems)
	}

	root, err := filepath.Abs(filepath.Join(outDir, id))
	if err != nil {
		return fetched{}, err
	}
	if _, err := os.Lstat(root); err == nil {
		status("Checking the existing " + root)
		if err := verifyExisting(root, id, crate); err != nil {
			return fetched{}, err
		}
		logf("%s is already here and matches its crate\n", root)
		return fetched{root, dataDir(root, files), statedBy(id, crate)}, nil
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

	partial, err := makePartial(root)
	if err != nil {
		return fetched{}, err
	}
	renamed := false
	defer func() {
		if !renamed {
			os.RemoveAll(partial)
		}
	}()
	dir, err := os.OpenRoot(partial)
	if err != nil {
		return fetched{}, err
	}
	started := time.Now()
	jobs := make([]job, len(files))
	for i, f := range files {
		jobs[i] = job{name: f.Rel, size: f.Size, run: func(ctx context.Context, progress func(int64)) error {
			if err := b.get(ctx, dir, f, progress); err != nil {
				return err
			}
			fetchHook(ctx, "downloaded")
			return nil
		}}
	}
	err = transferAll(ctx, newProgress(os.Stderr, len(jobs), total), jobs)
	if err == nil {
		err = dir.WriteFile(crateName, raw, 0o644)
	}
	dir.Close()
	if ctx.Err() != nil {
		return fetched{}, fmt.Errorf("interrupted; nothing was saved as %s, and the next fetch starts again", root)
	}
	if err != nil {
		return fetched{}, err
	}
	fetchHook(ctx, "renaming")
	if err := os.Rename(partial, root); err == nil {
		renamed = true
	} else if _, serr := os.Lstat(root); serr != nil {
		return fetched{}, err
	} else if err := verifyExisting(root, id, crate); err != nil {
		return fetched{}, err
	} else {
		logf("%s was saved meanwhile by another fetch, and matches its crate\n", root)
	}
	logDone(started, total)
	return fetched{root, dataDir(root, files), statedBy(id, crate)}, nil
}

// Each fetch downloads into a folder of its own, so several fetches of one dataset into one place never touch each other's.
func makePartial(root string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return "", err
	}
	if stale := stalePartials(root, 24*time.Hour); len(stale) > 0 {
		logf("%d download folders of %s are over a day old (%s); fetches that were killed leave them, and they can be deleted once no fetch of it is running\n", len(stale), filepath.Base(root), strings.Join(stale, ", "))
	}
	for {
		partial := root + ".partial-" + strings.ToLower(rand.Text()[:8])
		if err := os.Mkdir(partial, 0o755); !errors.Is(err, fs.ErrExist) {
			return partial, err
		}
	}
}

func stalePartials(root string, age time.Duration) []string {
	var stale []string
	matches, _ := filepath.Glob(root + ".partial-*")
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() && time.Since(info.ModTime()) > age {
			stale = append(stale, m)
		}
	}
	return stale
}

func verifyExisting(root, id string, crate storedCrate) error {
	problem, err := verifyDir(root, crate)
	if err != nil {
		return err
	}
	if problem != "" {
		return fmt.Errorf("%s already exists but is not dataset %s (%s); move it aside or fetch with another --out", root, id, problem)
	}
	return nil
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

func localNameProblems(files []crateFile, goos string) []string {
	var problems []string
	fold := goos == "windows" || goos == "darwin"
	rels := make([]string, len(files))
	for i, f := range files {
		rels[i] = f.Rel
		if goos == "windows" {
			if p := nameProblem(f.Rel); p != "" {
				problems = append(problems, f.Rel+": "+p)
			}
		}
		if clashesWithCrate(f.Rel, fold) {
			problems = append(problems, f.Rel+": would be replaced by the dataset's crate, which is saved beside it")
		}
	}
	if fold {
		problems = append(problems, sameNameProblems(rels, "this computer")...)
	}
	return problems
}

// Returns why root does not hold exactly these files and crate, or "" if it does.
func verifyDir(root string, crate storedCrate) (string, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	want := map[string]bool{crateName: true}
	for _, f := range crate.Files {
		want[f.Rel] = true
		if problem, err := checkFile(dir, f); problem != "" || err != nil {
			return problem, err
		}
	}
	local, err := dir.ReadFile(crateName)
	if err != nil || !bytes.Equal(local, crate.raw) {
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
