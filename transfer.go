package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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

func upload(ctx context.Context, t target, input string) (string, error) {
	source, files, err := datasetFiles(input)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("%s contains no files to upload", input)
	}

	id := newID()
	crate, err := buildCrate(id, source, files, t, time.Now())
	if err != nil {
		return "", err
	}

	cc, err := t.client()
	if err != nil {
		return "", err
	}

	var total int64
	for _, f := range files {
		total += f.Size
	}
	fmt.Printf("Uploading %d files (%s) from %s as %s\n", len(files), humanBytes(total), source, id)

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
			fmt.Printf("  [%d/%d] %s\n", done.Add(1), len(files), f.Rel)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return "", err
	}

	_, err = cc.NewBlockBlobClient(id+"/ro-crate-metadata.json").UploadBuffer(ctx, crate, &blockblob.UploadBufferOptions{
		HTTPHeaders: &blob.HTTPHeaders{BlobContentType: to.Ptr("application/json")},
	})
	if err != nil {
		return "", fmt.Errorf("uploading ro-crate-metadata.json: %w", err)
	}

	fmt.Printf("Done in %s (%s/s)\n", time.Since(started).Round(time.Millisecond), humanBytes(rate(total, time.Since(started))))
	return id, nil
}

func download(ctx context.Context, t target, id, outDir string) error {
	if !validID.MatchString(id) {
		return fmt.Errorf("%q is not a dataset ID", id)
	}
	cc, err := t.client()
	if err != nil {
		return err
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
			return fmt.Errorf("listing %s: %w", id, err)
		}
		for _, b := range page.Segment.BlobItems {
			if !isDirectoryMarker(b) {
				blobs = append(blobs, b)
			}
		}
	}
	if len(blobs) == 0 {
		return fmt.Errorf("no dataset %s in %s", id, t.containerURL())
	}

	root, err := filepath.Abs(filepath.Join(outDir, id))
	if err != nil {
		return err
	}

	var total int64
	for _, b := range blobs {
		total += deref(b.Properties.ContentLength)
	}
	fmt.Printf("Downloading %d files (%s) to %s\n", len(blobs), humanBytes(total), root)

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
			fmt.Printf("  [%d/%d] %s\n", done.Add(1), len(blobs), strings.TrimPrefix(name, prefix))
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	fmt.Printf("Done in %s (%s/s)\n", time.Since(started).Round(time.Millisecond), humanBytes(rate(total, time.Since(started))))
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
