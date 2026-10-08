package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/pkg/browser"
	"golang.org/x/sync/errgroup"
)

//go:embed browse.html
var browsePage []byte

type catalogue struct {
	Datasets []map[string]any         `json:"datasets"`
	Files    map[string][]catalogFile `json:"files"`
	Labels   map[string]string        `json:"labels"`
	crates   map[string]string
}

type catalogFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

func browse(ctx context.Context, o options) error {
	cred, _, err := credential(ctx, o.tenant)
	if err != nil {
		return err
	}
	status("Finding the storage account tagged " + o.tag)
	matches, err := findAccounts(ctx, cred, o.tag)
	status("")
	if err != nil {
		return err
	}
	chosen, err := chooseAccount(matches, o.tag, o.account, terminalPicker(ctx))
	if err != nil {
		return err
	}
	svc, err := service.NewClient(fmt.Sprintf("https://%s.blob.core.windows.net/", chosen.Name), cred, nil)
	if err != nil {
		return err
	}

	containers := strings.FieldsFunc(o.container, func(r rune) bool { return r == ',' })
	if len(containers) == 0 {
		if containers, err = listContainers(ctx, svc); err != nil {
			return fmt.Errorf("listing the containers in %s needs read access to the whole account; name them with --container instead: %w", chosen.Name, err)
		}
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return err
	}

	cat := catalogue{Files: map[string][]catalogFile{}, Labels: map[string]string{}, crates: map[string]string{}}
	for _, name := range containers {
		status("Reading crates in " + name)
		cache := filepath.Join(cacheRoot, "lbf", "crates", chosen.Name, name)
		ids, err := syncCrates(ctx, svc.NewContainerClient(name), cache)
		if bloberror.HasCode(err, bloberror.AuthorizationPermissionMismatch, bloberror.AuthorizationFailure) {
			logf("Skipping %s: you cannot read it\n", name)
			continue
		}
		if err != nil {
			return err
		}
		for _, id := range ids {
			path := filepath.Join(cache, id, crateName)
			raw, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			row, files, names, err := flattenCrate(raw)
			if err != nil {
				logf("Skipping %s/%s: %v\n", name, id, err)
				continue
			}
			row["id"], row["container"] = id, name
			row["fetch"] = fetchCommand(id, name, o.tag, chosen.Name)
			cat.Datasets = append(cat.Datasets, row)
			cat.Files[name+"/"+id] = files
			cat.crates[name+"/"+id] = path
			for iri, l := range names {
				if cat.Labels[iri] == "" {
					cat.Labels[iri] = l
				}
			}
		}
	}
	status("")
	logf("%d datasets in %s\n", len(cat.Datasets), chosen.Name)
	return serveCatalogue(ctx, cat, chosen.Name, o.port)
}

func listContainers(ctx context.Context, svc *service.Client) ([]string, error) {
	var names []string
	pager := svc.NewListContainersPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range page.ContainerItems {
			names = append(names, *c.Name)
		}
	}
	return names, nil
}

// Crates never change once written, so a cached one is never stale; folders without one are asked again each time.
func syncCrates(ctx context.Context, cc *container.Client, cache string) ([]string, error) {
	var ids []string
	pager := cc.NewListBlobsHierarchyPager("/", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range page.Segment.BlobPrefixes {
			ids = append(ids, strings.TrimSuffix(*p.Name, "/"))
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(16)
	for _, id := range ids {
		path := filepath.Join(cache, id, crateName)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		g.Go(func() error {
			crate, err := downloadBuffer(gctx, cc, id+"/"+crateName)
			if bloberror.HasCode(err, bloberror.BlobNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			return os.WriteFile(path, crate, 0o644)
		})
	}
	return ids, g.Wait()
}

func fetchCommand(id, container, tag, account string) string {
	cmd := "lbf fetch " + id
	if container != "bronze" {
		cmd += " --container " + container
	}
	if tag != "tag=storage" {
		cmd += " --tag " + tag
	}
	return cmd + " --account " + account
}

// One row per root Dataset and the run that made it; a linked IRI stays the value, as it identifies the entity across crates.
func flattenCrate(raw []byte) (map[string]any, []catalogFile, map[string]string, error) {
	var doc struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, nil, fmt.Errorf("its %s is not valid JSON: %w", crateName, err)
	}
	graph := map[string]map[string]any{}
	for _, e := range doc.Graph {
		if id, ok := e["@id"].(string); ok {
			graph[id] = e
		}
	}
	rootID := "./"
	if about, ok := graph[crateName]["about"].(map[string]any); ok {
		rootID, _ = about["@id"].(string)
	}
	root, ok := graph[rootID]
	if !ok {
		return nil, nil, nil, fmt.Errorf("its %s has no root dataset", crateName)
	}

	names := map[string]string{}
	label := func(v any) string {
		ref, ok := v.(map[string]any)
		if !ok {
			return fmt.Sprint(v)
		}
		id := fmt.Sprint(ref["@id"])
		e, ok := graph[id]
		if !ok {
			e = ref
		}
		name, hasName := e["name"]
		version, versioned := e["version"]
		if hasName && versioned {
			name = fmt.Sprint(name, " ", version)
		}
		// One IRI for every version, such as a repository, would show every crate the first version seen.
		if u, err := url.Parse(id); err == nil && u.Scheme != "" && (!versioned || strings.Contains(id, fmt.Sprint(version))) {
			if hasName && fmt.Sprint(e["name"]) != id {
				names[id] = fmt.Sprint(name)
			}
			return id
		}
		if hasName {
			return fmt.Sprint(name)
		}
		for _, k := range []string{"identifier", "value", "contentUrl"} {
			if s, ok := e[k]; ok {
				return fmt.Sprint(s)
			}
		}
		return id
	}
	labels := func(v any) any {
		var parts []string
		for _, item := range asList(v) {
			parts = append(parts, label(item))
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return parts
	}

	row := map[string]any{}
	for k, v := range root {
		switch k {
		case "@id", "@type", "hasPart", "mentions":
		case "additionalProperty":
			for _, ref := range asList(v) {
				pv, _ := ref.(map[string]any)
				if e, ok := graph[fmt.Sprint(pv["@id"])]; ok {
					pv = e
				}
				if name, ok := pv["name"].(string); ok {
					row[name] = fmt.Sprint(pv["value"])
				}
			}
		default:
			row[k] = labels(v)
		}
	}
	for _, e := range graph {
		if !slices.Contains(typesOf(e), "CreateAction") || !slices.ContainsFunc(asList(e["result"]), func(r any) bool { return refID(r) == rootID }) {
			continue
		}
		for k, v := range e {
			if k != "@id" && k != "@type" && k != "result" {
				row["run_"+k] = labels(v)
			}
		}
	}

	var files []catalogFile
	var total int64
	for _, ref := range asList(root["hasPart"]) {
		e, ok := graph[refID(ref)]
		if !ok || !slices.Contains(typesOf(e), "File") {
			continue
		}
		var size int64
		fmt.Sscan(fmt.Sprint(e["contentSize"]), &size)
		sha, _ := e["sha256"].(string)
		files = append(files, catalogFile{Path: refID(ref), Size: size, SHA256: sha})
		total += size
	}
	row["files"], row["size_bytes"] = len(files), total
	return row, files, names, nil
}

func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	if v == nil {
		return nil
	}
	return []any{v}
}

func refID(v any) string {
	if m, ok := v.(map[string]any); ok {
		s, _ := m["@id"].(string)
		return s
	}
	return ""
}

func typesOf(e map[string]any) []string {
	var types []string
	for _, t := range asList(e["@type"]) {
		types = append(types, fmt.Sprint(t))
	}
	return types
}

func catalogueHandler(cat catalogue) (http.Handler, error) {
	data, err := json.Marshal(cat)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(browsePage)
	})
	mux.HandleFunc("GET /catalogue.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})
	mux.HandleFunc("GET /crate/{container}/{id}", func(w http.ResponseWriter, r *http.Request) {
		path, ok := cat.crates[r.PathValue("container")+"/"+r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		http.ServeFile(w, r, path)
	})
	return mux, nil
}

func serveCatalogue(ctx context.Context, cat catalogue, account, port string) error {
	mux, err := catalogueHandler(cat)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String() + "/"
	fmt.Printf("Browsing %s at %s (Ctrl-C to stop)\n", account, url)
	browser.Stdout, browser.Stderr = io.Discard, io.Discard
	_ = browser.OpenURL(url)

	srv := &http.Server{Handler: mux}
	context.AfterFunc(ctx, func() { srv.Close() })
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
