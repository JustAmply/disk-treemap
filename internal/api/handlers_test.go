package api

import (
	"context"
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

	cfg := config.Config{
		AnalyzeRoot:         root,
		DataDir:             dataDir,
		ScanMaxConcurrency:  2,
		MaxChildrenPerQuery: 100,
	}

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

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
	if err := writer.InsertNode(context.Background(), scanID, store.Node{
		Path:       root,
		ParentPath: "",
		Name:       filepath.Base(root),
		Kind:       "dir",
		SizeBytes:  0,
		MtimeUnix:  time.Now().Unix(),
	}); err != nil {
		_ = writer.Rollback()
		t.Fatalf("insert root node: %v", err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := st.CompleteScan(context.Background(), scanID, "completed", time.Now().UTC(), 0, 1, 0, ""); err != nil {
		t.Fatalf("complete scan: %v", err)
	}

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
