package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"golang.org/x/sync/errgroup"
)

// One dataset's blobs: everything publish and fetch ask of Azure. Blobs are only ever created, never replaced.
type blobs struct {
	t  target
	cc *container.Client
	id string
}

func (t target) blobs(id string) (blobs, error) {
	cc, err := container.NewClientWithNoCredential(t.containerURL()+"?"+t.SAS, nil)
	return blobs{t, cc, id}, err
}

func (b blobs) name(rel string) string { return b.id + "/" + rel }

// The dataset's files by path within it, with the sha256 each was stored with.
func (b blobs) list(ctx context.Context) (map[string]crateFile, error) {
	prefix := b.id + "/"
	stored := map[string]crateFile{}
	pager := b.cc.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Prefix:  new(prefix),
		Include: container.ListBlobsInclude{Metadata: true},
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", b.id, err)
		}
		for _, item := range page.Segment.BlobItems {
			if !isDirectoryMarker(item) {
				rel := strings.TrimPrefix(*item.Name, prefix)
				stored[rel] = crateFile{Rel: rel, Size: deref(item.Properties.ContentLength), SHA256: metadata(item.Metadata, "sha256")}
			}
		}
	}
	return stored, nil
}

func (b blobs) read(ctx context.Context, rel string) ([]byte, error) {
	resp, err := b.cc.NewBlobClient(b.name(rel)).DownloadStream(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (b blobs) has(ctx context.Context, rel string) (bool, error) {
	_, err := b.cc.NewBlobClient(b.name(rel)).GetProperties(ctx, nil)
	if bloberror.HasCode(err, bloberror.BlobNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Commits with its sha256 only if the file did not change while being sent, so stored metadata never lies.
func (b blobs) putFile(ctx context.Context, f *localFile, progress func(int64)) error {
	fh, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer fh.Close()
	h := sha256.New()
	r := &countingReader{r: io.TeeReader(io.LimitReader(fh, f.Size), h), progress: progress}
	bb := b.cc.NewBlockBlobClient(b.name(f.Rel))
	size := max(blockSize, (f.Size+maxBlocks-1)/maxBlocks)

	// Uncommitted blocks are shared by everyone writing this blob, so each attempt stages under its own IDs.
	attempt := make([]byte, 8)
	_, _ = rand.Read(attempt)
	var ids []string
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(blockWorkers)
	for off := int64(0); off < f.Size && gctx.Err() == nil; off += size {
		buf := make([]byte, min(size, f.Size-off))
		if _, err := io.ReadFull(r, buf); err != nil {
			break
		}
		blockID := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%x-%08d", attempt, len(ids)))
		ids = append(ids, blockID)
		g.Go(func() error {
			_, err := bb.StageBlock(gctx, blockID, streaming.NopCloser(bytes.NewReader(buf)), nil)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("uploading %s: %w", f.Rel, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unchanged(f, r.n); err != nil {
		return err
	}

	sum := hex.EncodeToString(h.Sum(nil))
	meta := map[string]*string{"sha256": &sum}
	if u := strings.TrimSpace(b.t.User); u != "" && !strings.ContainsFunc(u, func(c rune) bool { return c < ' ' || c > '~' }) {
		meta["uploader"] = &u
	}
	_, err = bb.CommitBlockList(ctx, ids, &blockblob.CommitBlockListOptions{Metadata: meta, AccessConditions: ifAbsent})
	if bloberror.HasCode(err, bloberror.BlobAlreadyExists, bloberror.ConditionNotMet) {
		err = storedAs(ctx, bb, f.Size, sum)
		if errors.Is(err, errStoredDiffers) {
			return idTaken{fmt.Errorf("%s changed while this publish was writing it; is another publish of the same ID running?", b.name(f.Rel))}
		}
	}
	if err != nil {
		return fmt.Errorf("uploading %s: %w", f.Rel, err)
	}
	f.SHA256 = sum
	return nil
}

var ifAbsent = &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfNoneMatch: to.Ptr(azcore.ETagAny)}}

var errStoredDiffers = errors.New("a different blob is stored under this name")

// A commit whose response was lost is retried and refused, as is one that an earlier attempt sending the same bytes beat.
func storedAs(ctx context.Context, bb *blockblob.Client, size int64, sum string) error {
	props, err := bb.GetProperties(ctx, nil)
	if err != nil {
		return err
	}
	if deref(props.ContentLength) != size || metadata(props.Metadata, "sha256") != sum {
		return errStoredDiffers
	}
	return nil
}

var errAnotherCrate = errors.New("another publish of the same ID stored its crate first")

func (b blobs) putCrate(ctx context.Context, crate []byte) error {
	_, err := b.cc.NewBlockBlobClient(b.name(crateName)).UploadBuffer(ctx, crate, &blockblob.UploadBufferOptions{
		HTTPHeaders:      &blob.HTTPHeaders{BlobContentType: new("application/json")},
		AccessConditions: ifAbsent,
	})
	if bloberror.HasCode(err, bloberror.BlobAlreadyExists, bloberror.ConditionNotMet) {
		stored, err := b.read(ctx, crateName)
		if err != nil {
			return fmt.Errorf("reading the crate already stored as %s: %w", b.name(crateName), err)
		}
		if !bytes.Equal(stored, crate) {
			return errAnotherCrate
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("uploading %s: %w", crateName, err)
	}
	return nil
}

func (b blobs) get(ctx context.Context, dir *os.Root, f crateFile, progress func(int64)) error {
	fh, err := createIn(dir, f.Rel)
	if err != nil {
		return err
	}
	_, err = b.cc.NewBlobClient(b.name(f.Rel)).DownloadFile(ctx, fh, &blob.DownloadFileOptions{
		BlockSize:   blockSize,
		Concurrency: blockWorkers,
		Progress:    progress,
	})
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading %s: %w", b.name(f.Rel), err)
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

func checkAccess(ctx context.Context, t target, mode string) error {
	b, err := t.blobs("")
	if err != nil {
		return err
	}
	status("Checking access to " + t.Account + "/" + t.Container)
	defer status("")
	_, err = b.cc.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{MaxResults: to.Ptr[int32](1)}).NextPage(ctx)
	if bloberror.HasCode(err, bloberror.AuthorizationPermissionMismatch, bloberror.AuthorizationFailure, bloberror.InsufficientAccountPermissions) {
		return fmt.Errorf("you cannot list %s/%s; your login needs 'Storage Blob Data Reader' there", t.Account, t.Container)
	}
	if err != nil {
		return fmt.Errorf("checking access to %s/%s: %w", t.Account, t.Container, err)
	}
	if mode == "upload" {
		return b.checkWrite(ctx)
	}
	return nil
}

// An uncommitted block proves write access without creating a visible blob; Azure discards it after a week.
func (b blobs) checkWrite(ctx context.Context) error {
	blockID := base64.StdEncoding.EncodeToString([]byte("lbf-write-check"))
	_, err := b.cc.NewBlockBlobClient(".lbf-check").StageBlock(ctx, blockID, streaming.NopCloser(bytes.NewReader([]byte{0})), nil)
	if bloberror.HasCode(err, bloberror.AuthorizationPermissionMismatch, bloberror.AuthorizationFailure, bloberror.InsufficientAccountPermissions) {
		return fmt.Errorf("you cannot write to %s/%s, so nothing was uploaded; your login needs 'Storage Blob Data Contributor' there", b.t.Account, b.t.Container)
	}
	if err != nil {
		return fmt.Errorf("checking write access to %s/%s: %w", b.t.Account, b.t.Container, err)
	}
	return nil
}

// Hierarchical-namespace accounts list directories as zero-length blobs.
func isDirectoryMarker(b *container.BlobItem) bool {
	return metadata(b.Metadata, "hdi_isfolder") == "true"
}

func metadata(m map[string]*string, key string) string {
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return deref(v)
		}
	}
	return ""
}
