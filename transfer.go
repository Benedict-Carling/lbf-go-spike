package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

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
	pub := publication{ID: newID(), Source: source, Files: files, Provenance: prov, Profile: prof}
	if err := prof.validate(profileView(pub.ID, prov, files)); err != nil {
		return publication{}, err
	}
	logf("[profile] meets %s\n", prof.ID)
	return pub, nil
}

func (p publication) crate(t target) ([]byte, error) {
	return buildCrate(p.ID, p.Source, p.Files, t, p.Provenance, p.Profile.ID, time.Now())
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

	id, files := pub.ID, pub.Files
	if err := checkWrite(ctx, cc, t, id); err != nil {
		return err
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	logf("Uploading %d files (%s) from %s as %s\n", len(files), humanBytes(total), pub.Source, id)

	var done atomic.Int64
	started := time.Now()
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelFiles)
	for _, f := range files {
		g.Go(func() error {
			fh, err := os.Open(f.Path)
			if err != nil {
				return err
			}
			defer fh.Close()
			_, err = cc.NewBlockBlobClient(id+"/"+f.Rel).UploadFile(gctx, fh, &blockblob.UploadFileOptions{
				BlockSize:   blockSize,
				Concurrency: blockWorkers,
				AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{
					IfNoneMatch: to.Ptr(azcore.ETagAny),
				}},
			})
			if bloberror.HasCode(err, bloberror.BlobAlreadyExists, bloberror.ConditionNotMet) {
				return fmt.Errorf("%s already exists in the container; refusing to overwrite", id+"/"+f.Rel)
			}
			if err != nil {
				return fmt.Errorf("uploading %s: %w", f.Rel, err)
			}
			logf("  [%d/%d] %s\n", done.Add(1), len(files), f.Rel)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	_, err = cc.NewBlockBlobClient(id+"/ro-crate-metadata.json").UploadBuffer(ctx, crate, &blockblob.UploadBufferOptions{
		HTTPHeaders: &blob.HTTPHeaders{BlobContentType: to.Ptr("application/json")},
	})
	if err != nil {
		return fmt.Errorf("uploading ro-crate-metadata.json: %w", err)
	}

	logf("Done in %s (%s/s)\n", time.Since(started).Round(time.Millisecond), humanBytes(rate(total, time.Since(started))))
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

	var done atomic.Int64
	started := time.Now()
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelFiles)
	for _, b := range blobs {
		g.Go(func() error {
			name := *b.Name
			local, err := localPath(root, strings.TrimPrefix(name, prefix))
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
				return err
			}
			fh, err := os.Create(local)
			if err != nil {
				return err
			}
			_, err = cc.NewBlobClient(name).DownloadFile(gctx, fh, &blob.DownloadFileOptions{
				BlockSize:   blockSize,
				Concurrency: blockWorkers,
			})
			if cerr := fh.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return fmt.Errorf("downloading %s: %w", name, err)
			}
			logf("  [%d/%d] %s\n", done.Add(1), len(blobs), strings.TrimPrefix(name, prefix))
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return "", err
	}

	logf("Done in %s (%s/s)\n", time.Since(started).Round(time.Millisecond), humanBytes(rate(total, time.Since(started))))
	return root, nil
}

// Staging an uncommitted block proves write access without creating a visible blob;
// the later upload of the same name discards it.
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

func localPath(root, rel string) (string, error) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	r, err := filepath.Rel(root, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", errors.New("blob name escapes the download directory: " + rel)
	}
	return p, nil
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
