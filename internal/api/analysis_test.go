package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/justamply/disk-treemap/internal/app"
	"github.com/justamply/disk-treemap/internal/store"
	"github.com/justamply/disk-treemap/internal/testsupport"
)

func TestSnapshotAnalysisAndExports(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	cfg := testsupport.TestConfig(root, data)
	st := testsupport.OpenStore(t, data)
	svc := app.NewService(cfg, st)
	mux := http.NewServeMux()
	NewHandler(svc, cfg, "../../web").Register(mux)
	node := func(name, kind string, size, mtime int64) store.Node {
		p := filepath.Join(root, name)
		parent := filepath.Dir(p)
		if name == "" {
			parent = ""
		}
		return store.Node{Path: p, ParentPath: parent, Name: filepath.Base(p), Kind: kind, SizeBytes: size, MtimeUnix: mtime}
	}
	before := testsupport.CompletedScan(t, st, root, []store.Node{node("", "dir", 30, 1), node("old.txt", "file", 10, 1), node("keep.bin", "file", 20, 1)})
	after := testsupport.CompletedScan(t, st, root, []store.Node{node("", "dir", 70, 1), node("new.txt", "file", 10, 2), node("keep.bin", "file", 30, 1), node("sub", "dir", 30, 1), node("sub/a.TXT", "file", 30, 1)})
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	prefix := "/api/v1/scans/" + strconv.FormatInt(after, 10)
	pathQuery := "path=" + url.QueryEscape(root)
	for _, endpoint := range []string{"diff?base=" + strconv.FormatInt(before, 10) + "&", "extensions?", "export?format=csv&", "export?format=json&"} {
		rec := get(prefix + "/" + endpoint + "path=" + url.QueryEscape(filepath.Dir(root)))
		if rec.Code != 400 {
			t.Fatalf("outside root %s: %d %s", endpoint, rec.Code, rec.Body.String())
		}
	}
	rec := get(prefix + "/explore?" + pathQuery + "&limit=1&offset=1")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var view app.FolderViewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Items) != 1 || !view.HasMore || view.NextOffset != 2 || view.Summary.MatchingItemCount != 3 {
		t.Fatalf("page: %+v", view)
	}
	rec = get(prefix + "/largest?" + pathQuery + "&extension=.txt&modified_before=2&limit=1")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var largest app.LargestResponse
	json.Unmarshal(rec.Body.Bytes(), &largest)
	if len(largest.Items) != 1 || largest.Items[0].Name != "a.TXT" || largest.HasMore {
		t.Fatalf("filtered recursive search: %+v", largest)
	}
	rec = get(prefix + "/extensions?" + pathQuery)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var groups struct {
		Items []app.ExtensionSummary `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &groups)
	if len(groups.Items) != 2 || groups.Items[0].Extension != ".txt" || groups.Items[0].Count != 2 || groups.Items[0].TotalBytes != 40 {
		t.Fatalf("groups: %+v", groups)
	}
	rec = get(prefix + "/diff?" + pathQuery + "&base=" + strconv.FormatInt(before, 10))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var diff app.SnapshotDiff
	json.Unmarshal(rec.Body.Bytes(), &diff)
	changes := map[string]store.SnapshotChange{}
	for _, c := range diff.Items {
		changes[filepath.Base(c.Path)] = c
	}
	if changes["old.txt"].Change != "removed" || changes["new.txt"].Change != "added" || changes["keep.bin"].DeltaBytes != 10 {
		t.Fatalf("diff: %+v", diff)
	}
	rec = get(prefix + "/export?" + pathQuery + "&scope=recursive&format=csv&limit=1")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil || len(records) != 5 {
		t.Fatalf("full export must ignore interactive page limit: %v %v", records, err)
	}
	rec = get(prefix + "/export?" + pathQuery + "&scope=recursive&format=json&extension=.txt")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var export struct {
		Items  []store.Node `json:"items"`
		ScanID int64        `json:"scan_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	if len(export.Items) != 2 || export.ScanID != after {
		t.Fatalf("export: %+v", export)
	}
	rec = get("/api/v1/scans")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var history struct {
		Scans []store.ScanRun `json:"scans"`
	}
	json.Unmarshal(rec.Body.Bytes(), &history)
	if len(history.Scans) != 2 || history.Scans[0].ID != after {
		t.Fatalf("history: %+v", history)
	}
	st.SetSnapshotRetention(2)
	third := testsupport.CompletedScan(t, st, root, []store.Node{node("", "dir", 0, 1)})
	if _, err := st.PruneOperationalScans(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, err := st.ListSnapshots(context.Background())
	if err != nil || len(runs) != 2 || runs[0].ID != third || runs[1].ID != after {
		t.Fatalf("retention: %+v %v", runs, err)
	}
}

func TestAnalysisRejectsInvalidFilters(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	cfg := testsupport.TestConfig(root, data)
	st := testsupport.OpenStore(t, data)
	svc := app.NewService(cfg, st)
	mux := http.NewServeMux()
	NewHandler(svc, cfg, "../../web").Register(mux)
	for _, query := range []string{"offset=-1", "modified_before=-1", "extension=txt", "extension=.a/b"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/scans/1/extensions?path="+url.QueryEscape(root)+"&"+query, nil))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", query, rec.Code, rec.Body.String())
		}
	}
}
func TestDiffAtFilesystemRoot(t *testing.T) {
	data := t.TempDir()
	root := filepath.VolumeName(data) + string(filepath.Separator)
	st := testsupport.OpenStore(t, data)
	snapshot := func(size int64) int64 {
		return testsupport.CompletedScan(t, st, root, []store.Node{
			{Path: root, Name: filepath.Base(root), Kind: "dir", SizeBytes: size},
			{Path: filepath.Join(root, "file.bin"), ParentPath: root, Name: "file.bin", Kind: "file", SizeBytes: size},
		})
	}
	before, after := snapshot(10), snapshot(20)
	svc := app.NewService(testsupport.TestConfig(root, data), st)
	diff, err := svc.CompareSnapshots(context.Background(), before, after, app.FolderViewRequest{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range diff.Items {
		if item.Path == filepath.Join(root, "file.bin") {
			found = true
			if item.DeltaBytes != 10 {
				t.Fatalf("wrong delta: %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("filesystem root produced malformed child paths: %+v", diff)
	}
}
