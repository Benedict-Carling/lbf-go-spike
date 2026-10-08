package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
)

// Azurite's published development account.
const (
	emulatorAccount = "devstoreaccount1"
	emulatorKey     = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
	emulatorURL     = "http://127.0.0.1:10000/" + emulatorAccount
)

// A fresh container on a running Azurite (npx -p azurite azurite-blob --inMemoryPersistence),
// reached through a container SAS with the given permissions, as lbf reaches Azure.
func emulator(t *testing.T, perms sas.ContainerPermissions) target {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:10000", time.Second)
	if err != nil {
		if os.Getenv("LBF_AZURITE") != "" {
			t.Fatal("LBF_AZURITE is set but Azurite is not listening on 127.0.0.1:10000")
		}
		t.Skip("Azurite is not running")
	}
	conn.Close()

	cred, err := container.NewSharedKeyCredential(emulatorAccount, emulatorKey)
	must(t, err)
	name := strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
	name = fmt.Sprintf("%.50s-%d", name, time.Now().UnixNano()%1e9)
	cc, err := container.NewClientWithSharedKeyCredential(emulatorURL+"/"+name, cred, nil)
	must(t, err)
	_, err = cc.Create(context.Background(), nil)
	must(t, err)
	t.Cleanup(func() { cc.Delete(context.Background(), nil) })

	expiry := time.Now().Add(time.Hour)
	qp, err := sas.BlobSignatureValues{
		Protocol:      sas.ProtocolHTTPSandHTTP,
		ExpiryTime:    expiry,
		Permissions:   perms.String(),
		ContainerName: name,
	}.SignWithSharedKey(cred)
	must(t, err)
	return target{Account: emulatorAccount, Container: name, SAS: qp.Encode(), Expiry: expiry, User: "tester@example.org", endpoint: emulatorURL}
}

var uploadPerms = sas.ContainerPermissions{Read: true, List: true, Write: true}

func dataset(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run1")
	for rel, body := range files {
		writeFile(t, filepath.Join(dir, rel), body)
	}
	return dir
}

func propertiesFile(t *testing.T, body string) provenanceFlags {
	t.Helper()
	path := filepath.Join(t.TempDir(), "properties.json")
	writeFile(t, path, body)
	return provenanceFlags{properties: path}
}

func prepared(t *testing.T, dir, id string, prov provenanceFlags) publication {
	t.Helper()
	pub, err := preparePublication(dir, prov, "", id)
	must(t, err)
	return pub
}

func blobsOf(t *testing.T, tg target, id string) blobs {
	t.Helper()
	b, err := tg.blobs(id)
	must(t, err)
	return b
}

func storedNow(t *testing.T, tg target, id string) map[string]crateFile {
	t.Helper()
	stored, err := blobsOf(t, tg, id).list(context.Background())
	must(t, err)
	return stored
}

// Stores some of a dataset's files as an attempt that stopped partway would have.
func earlierAttempt(t *testing.T, tg target, id string, files map[string]string, stored ...string) {
	t.Helper()
	b := blobsOf(t, tg, id)
	pub := prepared(t, dataset(t, files), id, provenanceFlags{})
	for i := range pub.Files {
		if slices.Contains(stored, pub.Files[i].Rel) {
			must(t, b.putFile(context.Background(), &pub.Files[i], func(int64) {}))
		}
	}
}

func etag(t *testing.T, tg target, name string) string {
	t.Helper()
	props, err := blobsOf(t, tg, "").cc.NewBlobClient(name).GetProperties(context.Background(), nil)
	must(t, err)
	return string(*props.ETag)
}

func TestPublishResumesAnEarlierAttempt(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	id := newID()
	files := map[string]string{"a.txt": "a", "sub/b.csv": "b,c", "c.txt": "c"}
	earlierAttempt(t, tg, id, files, "run1/a.txt", "run1/sub/b.csv")
	before := etag(t, tg, id+"/run1/a.txt")

	must(t, upload(ctx, tg, prepared(t, dataset(t, files), id, provenanceFlags{})))

	if etag(t, tg, id+"/run1/a.txt") != before {
		t.Error("a file already stored was sent again")
	}
	for name, s := range storedNow(t, tg, id) {
		if name != crateName && len(s.SHA256) != 64 {
			t.Errorf("%s stored without its sha256 (%q)", name, s.SHA256)
		}
	}
	got, err := download(ctx, tg, id, t.TempDir())
	must(t, err)
	body, err := os.ReadFile(filepath.Join(got.dataPath, "c.txt"))
	must(t, err)
	if string(body) != "c" {
		t.Fatalf("fetched %q", body)
	}
}

func TestResumeNeverChangesAStoredFile(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	id := newID()
	earlierAttempt(t, tg, id, map[string]string{"same.txt": "s", "changed.csv": "run at 10:00", "gone.txt": "g", "short.txt": "abc"},
		"run1/same.txt", "run1/changed.csv", "run1/gone.txt", "run1/short.txt")
	before := storedNow(t, tg, id)

	err := upload(ctx, tg, prepared(t, dataset(t, map[string]string{"same.txt": "s", "changed.csv": "run at 11:30", "short.txt": "ab", "new.txt": "n"}), id, provenanceFlags{}))
	if _, ok := errors.AsType[idTaken](err); !ok {
		t.Fatalf("resumed over different files, or refused without exit code 3: %v", err)
	}
	for _, want := range []string{"run1/changed.csv: its content differs", "run1/gone.txt: stored, but not here", "run1/short.txt: 2 bytes here, 3 stored", "nothing was uploaded", "publish without --id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if after := storedNow(t, tg, id); !maps.Equal(after, before) {
		t.Fatalf("stored files changed:\nbefore %v\nafter  %v", before, after)
	}
}

func TestPublishAgainAfterItFinished(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	id := newID()
	dir := dataset(t, map[string]string{"a.txt": "a", "b/c.txt": "c"})
	props := propertiesFile(t, `{"sample_id": "S1"}`)

	must(t, upload(ctx, tg, prepared(t, dir, id, props)))
	crate := etag(t, tg, id+"/"+crateName)

	must(t, upload(ctx, tg, prepared(t, dir, id, props)))
	if etag(t, tg, id+"/"+crateName) != crate {
		t.Fatal("publishing the same dataset again rewrote its crate")
	}

	err := upload(ctx, tg, prepared(t, dir, id, propertiesFile(t, `{"sample_id": "S2"}`)))
	if _, ok := errors.AsType[idTaken](err); !ok || !strings.Contains(err.Error(), "already published, differently") {
		t.Fatalf("different properties: %v", err)
	}
	writeFile(t, filepath.Join(dir, "a.txt"), "A")
	err = upload(ctx, tg, prepared(t, dir, id, props))
	if err == nil || !strings.Contains(err.Error(), "a.txt: its content differs") {
		t.Fatalf("different content: %v", err)
	}
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "extra.txt"), "e")
	err = upload(ctx, tg, prepared(t, dir, id, props))
	if err == nil || !strings.Contains(err.Error(), "run1/extra.txt: not in it") {
		t.Fatalf("extra file: %v", err)
	}
}

func TestTwoPublishesOfOneIDCannotOverwriteEachOther(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	id := newID()
	b := blobsOf(t, tg, id)
	one := prepared(t, dataset(t, map[string]string{"a.txt": "a"}), id, provenanceFlags{})
	must(t, b.putFile(ctx, &one.Files[0], func(int64) {}))

	other := prepared(t, dataset(t, map[string]string{"a.txt": "b"}), id, provenanceFlags{})
	err := b.putFile(ctx, &other.Files[0], func(int64) {})
	if _, ok := errors.AsType[idTaken](err); !ok || !strings.Contains(err.Error(), "another publish") {
		t.Fatal(err)
	}
}

func TestRacingUploadsNeverStoreAnotherFilesBytes(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	for i := range 20 {
		b := blobsOf(t, tg, fmt.Sprintf("race%d", i))
		pubs := []publication{
			prepared(t, dataset(t, map[string]string{"f.bin": strings.Repeat("a", blockSize+1000)}), "", provenanceFlags{}),
			prepared(t, dataset(t, map[string]string{"f.bin": strings.Repeat("b", blockSize+1000)}), "", provenanceFlags{}),
		}
		done := make(chan error, 2)
		for j := range pubs {
			go func() { done <- b.putFile(ctx, &pubs[j].Files[0], func(int64) {}) }()
		}
		<-done
		<-done
		stored := storedNow(t, tg, b.id)["run1/f.bin"]
		body, err := b.read(ctx, "run1/f.bin")
		must(t, err)
		if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != stored.SHA256 {
			t.Fatalf("attempt %d: stored bytes do not match the stored sha256", i)
		}
	}
}

func TestPublishLargeFileInBlocks(t *testing.T) {
	tg := emulator(t, uploadPerms)
	ctx := context.Background()
	body := strings.Repeat("0123456789abcdef", (2*blockSize+123)/16)
	pub := prepared(t, dataset(t, map[string]string{"big.bin": body, "empty.txt": ""}), "", provenanceFlags{})
	must(t, upload(ctx, tg, pub))

	got, err := download(ctx, tg, pub.ID, t.TempDir())
	must(t, err)
	fetched, err := os.ReadFile(filepath.Join(got.dataPath, "big.bin"))
	must(t, err)
	if string(fetched) != body {
		t.Fatal("large file did not round-trip")
	}
}

func TestCheckAccess(t *testing.T) {
	ctx := context.Background()
	must(t, checkAccess(ctx, emulator(t, uploadPerms), "upload"))
	read := emulator(t, sas.ContainerPermissions{Read: true, List: true})
	must(t, checkAccess(ctx, read, "download"))
	if err := checkAccess(ctx, read, "upload"); err == nil || !strings.Contains(err.Error(), "cannot write") {
		t.Fatal(err)
	}
	if stored := storedNow(t, read, ""); len(stored) != 0 {
		t.Fatalf("check left %v behind", slices.Collect(maps.Keys(stored)))
	}
}

func TestPublishIDMustBeMinted(t *testing.T) {
	dir := dataset(t, map[string]string{"a.txt": "a"})
	for _, bad := range []string{"test", "20261001-fancy-dassie", "20261001-Fancy-dassie-eadb", "20261001-fancy-dassie-eadb/x"} {
		if _, err := preparePublication(dir, provenanceFlags{}, "", bad); err == nil {
			t.Errorf("--id %q accepted", bad)
		}
	}
	id := newID()
	if pub := prepared(t, dir, id, provenanceFlags{}); pub.ID != id {
		t.Fatalf("published as %s, not %s", pub.ID, id)
	}
	if o, _, err := parseArgs("publish", []string{dir, "--id", id}); err != nil || o.id != id {
		t.Fatalf("%v %q", err, o.id)
	}
}

func TestCrateProvenanceReadsBackWhatWasStated(t *testing.T) {
	prov := provenance{
		DerivedFrom: "20260101-x-y-0000",
		Instruments: []instrument{{"CellProfiler", "4.2.6", "https://cellprofiler.org"}, {"pipe", "1", "https://example.org/pipe"}},
		Properties:  map[string]string{"sample_id": "S1"},
		Name:        "Run 1",
		Description: "Cells, imaged",
	}
	prof := &profile{rules: []rule{{id: "https://example.org/p"}}}
	raw, err := publication{ID: "20260102-a-b-1234", Source: "/data/run1", Files: []localFile{{Rel: "run1/a", Size: 1}}, Provenance: prov, Profile: prof}.buildCrate(target{SubscriptionName: "sub"}, time.Now())
	must(t, err)
	got, conformsTo, err := statementOf(raw)
	must(t, err)
	if !got.sameAs(prov) || !slices.Equal(slices.Sorted(slices.Values(conformsTo)), []string{"https://example.org/p", processRunCrate}) {
		t.Fatalf("got %+v %v", got, conformsTo)
	}

	raw, err = publication{ID: "20260102-a-b-1234", Source: "/data/run1", Files: []localFile{{Rel: "run1/a", Size: 1}}, Provenance: provenance{Properties: map[string]string{}}}.buildCrate(target{}, time.Now())
	must(t, err)
	got, conformsTo, err = statementOf(raw)
	must(t, err)
	if !got.sameAs(provenance{Properties: map[string]string{}}) || len(conformsTo) != 0 {
		t.Fatalf("got %+v %v", got, conformsTo)
	}
}
