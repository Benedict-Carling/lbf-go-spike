package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
)

type publication struct {
	ID         string
	Source     string
	Files      []localFile
	Provenance provenance
	Profile    *profile
}

// Everything that can fail without Azure happens here, so a bad dataset never reaches sign-in or upload.
func preparePublication(input, provenancePath, profileDir string) (publication, error) {
	prov, err := readProvenance(provenancePath)
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
func (p publication) crate(t target) ([]byte, error) {
	published := time.Now()
	ids := p.Profile.ids()
	conformsTo := ids
	if p.Provenance.DerivedFrom != "" {
		conformsTo = append([]string{processRunCrate}, ids...)
	}
	if err := p.Profile.validate(p.view(t, published, conformsTo)); err != nil {
		return nil, err
	}
	logf("[profile] meets %s\n", strings.Join(ids, ", "))
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
	crate, err := pub.crate(t)
	if err != nil {
		return err
	}
	cc, err := t.client()
	if err != nil {
		return err
	}

	id := pub.ID
	if err := checkWrite(ctx, cc, t, id); err != nil {
		return err
	}
	var total int64
	for _, f := range pub.Files {
		total += f.Size
	}
	logf("Uploading %d files (%s) from %s as %s\n", len(pub.Files), humanBytes(total), pub.Source, id)

	started := time.Now()
	err = transferAll(ctx, pub.Files, func(ctx context.Context, f localFile) (string, error) {
		fh, err := os.Open(f.Path)
		if err != nil {
			return "", err
		}
		defer fh.Close()
		_, err = cc.NewBlockBlobClient(id+"/"+f.Rel).UploadFile(ctx, fh, &blockblob.UploadFileOptions{
			BlockSize:   blockSize,
			Concurrency: blockWorkers,
			AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{
				IfNoneMatch: to.Ptr(azcore.ETagAny),
			}},
		})
		if bloberror.HasCode(err, bloberror.BlobAlreadyExists, bloberror.ConditionNotMet) {
			return "", fmt.Errorf("%s already exists in the container; refusing to overwrite", id+"/"+f.Rel)
		}
		if err != nil {
			return "", fmt.Errorf("uploading %s: %w", f.Rel, err)
		}
		return f.Rel, nil
	})
	if err != nil {
		return err
	}

	_, err = cc.NewBlockBlobClient(id+"/ro-crate-metadata.json").UploadBuffer(ctx, crate, &blockblob.UploadBufferOptions{
		HTTPHeaders: &blob.HTTPHeaders{BlobContentType: to.Ptr("application/json")},
	})
	if err != nil {
		return fmt.Errorf("uploading ro-crate-metadata.json: %w", err)
	}
	logDone(started, total)
	return nil
}

func download(ctx context.Context, t target, id, outDir string) (string, error) {
	cc, err := t.client()
	if err != nil {
		return "", err
	}

	prefix := id + "/"
	var blobs []*container.BlobItem
	pager := cc.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Prefix:  to.Ptr(prefix),
		Include: container.ListBlobsInclude{Metadata: true},
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("listing %s: %w", id, err)
		}
		for _, b := range page.Segment.BlobItems {
			if !isDirectoryMarker(b) {
				blobs = append(blobs, b)
			}
		}
	}
	if len(blobs) == 0 {
		return "", fmt.Errorf("no dataset %s in %s", id, t.containerURL())
	}

	root, err := filepath.Abs(filepath.Join(outDir, id))
	if err != nil {
		return "", err
	}

	var total int64
	for _, b := range blobs {
		total += deref(b.Properties.ContentLength)
	}
	logf("Downloading %d files (%s) to %s\n", len(blobs), humanBytes(total), root)

	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer dir.Close()

	started := time.Now()
	err = transferAll(ctx, blobs, func(ctx context.Context, b *container.BlobItem) (string, error) {
		rel := strings.TrimPrefix(*b.Name, prefix)
		fh, err := createIn(dir, rel)
		if err != nil {
			return "", err
		}
		_, err = cc.NewBlobClient(*b.Name).DownloadFile(ctx, fh, &blob.DownloadFileOptions{
			BlockSize:   blockSize,
			Concurrency: blockWorkers,
		})
		if cerr := fh.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return "", fmt.Errorf("downloading %s: %w", *b.Name, err)
		}
		return rel, nil
	})
	if err != nil {
		return "", err
	}
	logDone(started, total)
	return root, nil
}

func transferAll[T any](ctx context.Context, items []T, each func(context.Context, T) (string, error)) error {
	var done atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelFiles)
	for _, it := range items {
		g.Go(func() error {
			name, err := each(gctx, it)
			if err != nil {
				return err
			}
			logf("  [%d/%d] %s\n", done.Add(1), len(items), name)
			return nil
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
	_, err := cc.NewBlockBlobClient(id+"/ro-crate-metadata.json").StageBlock(ctx, blockID, streaming.NopCloser(bytes.NewReader([]byte{0})), nil)
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
