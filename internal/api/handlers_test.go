package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/justamply/disk-treemap/internal/app"
	"github.com/justamply/disk-treemap/internal/config"
	"github.com/justamply/disk-treemap/internal/store"
)

func TestChildrenRejectsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)

	st := newTestStore(t, dataDir)
	scanID := createCompletedScanWithNodes(t, st, root, []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 0, MtimeUnix: time.Now().Unix()},
	})

	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	outside := filepath.Clean(filepath.Join(root, ".."))
	endpoint := "/api/v1/scans/" + strconv.FormatInt(scanID, 10) + "/children?path=" + url.QueryEscape(outside)
	req := httptest.NewRequest(http.MethodGet, endpoint, nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConfigIncludesScanWriteAndHistoryFields(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	cfg.ScanWriteBatchSize = 256
	cfg.ScanProgressInterval = 125 * time.Millisecond
	cfg.ScanHistoryMaxRuns = 77

	st := newTestStore(t, dataDir)
	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if got := int(payload["scan_write_batch_size"].(float64)); got != 256 {
		t.Fatalf("expected batch size 256, got %d", got)
	}
	if got := int(payload["scan_progress_interval_ms"].(float64)); got != 125 {
		t.Fatalf("expected progress interval 125ms, got %d", got)
	}
	if got := int(payload["scan_history_max_runs"].(float64)); got != 77 {
		t.Fatalf("expected history max runs 77, got %d", got)
	}
}

func TestListScansSupportsStatusFilter(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)

	_ = createCompletedScanWithNodes(t, st, root, []store.Node{{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 1, MtimeUnix: 1}})
	failedID, err := st.CreateScanRun(context.Background(), root)
	if err != nil {
		t.Fatalf("create scan: %v", err)
	}
	if err := st.CompleteScan(context.Background(), failedID, "failed", time.Now().UTC(), 0, 0, 1, "x"); err != nil {
		t.Fatalf("complete failed scan: %v", err)
	}

	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans?status=failed&limit=10", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Items []store.ScanRun `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].Status != "failed" {
		t.Fatalf("unexpected scan list: %+v", payload.Items)
	}
}

func TestDeleteScanRemovesRun(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)
	scanID := createCompletedScanWithNodes(t, st, root, []store.Node{{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 0, MtimeUnix: 1}})

	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/scans/"+strconv.FormatInt(scanID, 10), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+strconv.FormatInt(scanID, 10), nil)
	recGet := httptest.NewRecorder()
	mux.ServeHTTP(recGet, reqGet)
	if recGet.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", recGet.Code)
	}
}

func TestDiffEndpointReturnsDirectoryDeltas(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)

	baseNodes := []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 300, MtimeUnix: 1},
		{Path: filepath.Join(root, "a"), ParentPath: root, Name: "a", Kind: "dir", SizeBytes: 100, MtimeUnix: 1},
		{Path: filepath.Join(root, "b"), ParentPath: root, Name: "b", Kind: "dir", SizeBytes: 200, MtimeUnix: 1},
	}
	targetNodes := []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 400, MtimeUnix: 1},
		{Path: filepath.Join(root, "a"), ParentPath: root, Name: "a", Kind: "dir", SizeBytes: 150, MtimeUnix: 1},
		{Path: filepath.Join(root, "c"), ParentPath: root, Name: "c", Kind: "dir", SizeBytes: 250, MtimeUnix: 1},
	}

	baseID := createCompletedScanWithNodes(t, st, root, baseNodes)
	targetID := createCompletedScanWithNodes(t, st, root, targetNodes)

	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	endpoint := "/api/v1/scans/" + strconv.FormatInt(targetID, 10) + "/diff?base_scan_id=" + strconv.FormatInt(baseID, 10) + "&path=" + url.QueryEscape(root)
	req := httptest.NewRequest(http.MethodGet, endpoint, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload app.DiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Items) != 3 {
		t.Fatalf("expected 3 diff items, got %d", len(payload.Items))
	}
}

func TestDiffEndpointAllowsPathMissingInBaseScan(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)

	baseNodes := []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 200, MtimeUnix: 1},
	}
	targetPath := filepath.Join(root, "newdir")
	targetNodes := []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 400, MtimeUnix: 1},
		{Path: targetPath, ParentPath: root, Name: "newdir", Kind: "dir", SizeBytes: 300, MtimeUnix: 1},
		{Path: filepath.Join(targetPath, "child"), ParentPath: targetPath, Name: "child", Kind: "dir", SizeBytes: 300, MtimeUnix: 1},
	}

	baseID := createCompletedScanWithNodes(t, st, root, baseNodes)
	targetID := createCompletedScanWithNodes(t, st, root, targetNodes)

	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	endpoint := "/api/v1/scans/" + strconv.FormatInt(targetID, 10) + "/diff?base_scan_id=" + strconv.FormatInt(baseID, 10) + "&path=" + url.QueryEscape(targetPath)
	req := httptest.NewRequest(http.MethodGet, endpoint, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload app.DiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("expected 1 diff item, got %d", len(payload.Items))
	}
	if payload.Items[0].Name != "child" || payload.Items[0].ChangeClass != "new" {
		t.Fatalf("unexpected diff item: %+v", payload.Items[0])
	}
}
func testConfig(root, dataDir string) config.Config {
	return config.Config{
		AnalyzeRoot:          root,
		DataDir:              dataDir,
		ScanMaxConcurrency:   2,
		ScanWriteBatchSize:   8,
		ScanProgressInterval: 25 * time.Millisecond,
		MaxChildrenPerQuery:  100,
		ScanHistoryMaxRuns:   50,
	}
}

func newTestStore(t *testing.T, dataDir string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return st
}

func createCompletedScanWithNodes(t *testing.T, st *store.Store, root string, nodes []store.Node) int64 {
	t.Helper()
	scanID, err := st.CreateScanRun(context.Background(), root)
	if err != nil {
		t.Fatalf("create scan: %v", err)
	}
	if err := st.MarkScanRunning(context.Background(), scanID, time.Now().UTC()); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	writer, err := st.BeginNodeWriter(context.Background(), scanID)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	if err := writer.InsertNodesBatch(context.Background(), scanID, nodes); err != nil {
		_ = writer.Rollback()
		t.Fatalf("insert nodes: %v", err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := st.CompleteScan(context.Background(), scanID, "completed", time.Now().UTC(), 0, int64(len(nodes)), 0, ""); err != nil {
		t.Fatalf("complete scan: %v", err)
	}
	return scanID
}

func TestDiffEndpointValidatesBaseScanIDFormat(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)
	targetID := createCompletedScanWithNodes(t, st, root, []store.Node{{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 1, MtimeUnix: 1}})

	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	endpoint := "/api/v1/scans/" + strconv.FormatInt(targetID, 10) + "/diff?base_scan_id=abc&path=" + url.QueryEscape(root)
	req := httptest.NewRequest(http.MethodGet, endpoint, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["error"] != "invalid base_scan_id" {
		t.Fatalf("expected invalid base_scan_id error, got %q", payload["error"])
	}
}
