package api

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/justamply/disk-treemap/internal/app"
	"github.com/justamply/disk-treemap/internal/config"
	"github.com/justamply/disk-treemap/internal/scancontrol"
	"github.com/justamply/disk-treemap/internal/store"
)

func TestBrandingAssetsUseRealTransparency(t *testing.T) {
	for _, asset := range []string{
		filepath.Join("..", "..", "web", "assets", "disk-treemap-logo.png"),
		filepath.Join("..", "..", "web", "assets", "favicon.png"),
	} {
		t.Run(filepath.Base(asset), func(t *testing.T) {
			f, err := os.Open(asset)
			if err != nil {
				t.Fatalf("open asset: %v", err)
			}
			defer f.Close()

			img, err := png.Decode(f)
			if err != nil {
				t.Fatalf("decode png: %v", err)
			}

			bounds := img.Bounds()
			corners := [][2]int{
				{bounds.Min.X, bounds.Min.Y},
				{bounds.Max.X - 1, bounds.Min.Y},
				{bounds.Min.X, bounds.Max.Y - 1},
				{bounds.Max.X - 1, bounds.Max.Y - 1},
			}
			for _, corner := range corners {
				_, _, _, alpha := img.At(corner[0], corner[1]).RGBA()
				if alpha != 0 {
					t.Fatalf("expected transparent corner pixel at %v, got alpha %d", corner, alpha)
				}
			}

			var transparentPixels int
			var opaquePixels int
			var lightFringePixels int
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					red, green, blue, alpha := img.At(x, y).RGBA()
					switch {
					case alpha == 0:
						transparentPixels++
					case alpha == 0xffff:
						opaquePixels++
					}
					if alpha > 0 && isLightNeutralPixel(red, green, blue) && hasTransparentNeighbor(img, x, y) {
						lightFringePixels++
					}
				}
			}

			if transparentPixels == 0 {
				t.Fatal("expected at least one fully transparent pixel")
			}
			if opaquePixels == 0 {
				t.Fatal("expected at least one fully opaque foreground pixel")
			}
			if lightFringePixels > 0 {
				t.Fatalf("expected no light opaque fringe pixels touching transparency, found %d", lightFringePixels)
			}
		})
	}
}

func isLightNeutralPixel(red, green, blue uint32) bool {
	minChannel := min(red, green, blue)
	maxChannel := max(red, green, blue)
	return minChannel >= 0x9191 && maxChannel-minChannel <= 0x3737
}

func hasTransparentNeighbor(img image.Image, x, y int) bool {
	bounds := img.Bounds()
	for neighborY := y - 1; neighborY <= y+1; neighborY++ {
		for neighborX := x - 1; neighborX <= x+1; neighborX++ {
			if neighborX == x && neighborY == y {
				continue
			}
			if neighborX < bounds.Min.X || neighborX >= bounds.Max.X || neighborY < bounds.Min.Y || neighborY >= bounds.Max.Y {
				continue
			}
			_, _, _, alpha := img.At(neighborX, neighborY).RGBA()
			if alpha == 0 {
				return true
			}
		}
	}
	return false
}

func TestStaticIndexReferencesBrandingAssets(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)
	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRec := httptest.NewRecorder()
	mux.ServeHTTP(indexRec, indexReq)

	if indexRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for index, got %d: %s", indexRec.Code, indexRec.Body.String())
	}

	body := indexRec.Body.String()
	for _, needle := range []string{
		`href="/assets/favicon.png"`,
		`src="/assets/disk-treemap-logo.png"`,
		`alt="Disk Treemap logo"`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected index to contain %q", needle)
		}
	}

	faviconReq := httptest.NewRequest(http.MethodGet, "/assets/favicon.png", nil)
	faviconRec := httptest.NewRecorder()
	mux.ServeHTTP(faviconRec, faviconReq)

	if faviconRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for favicon, got %d", faviconRec.Code)
	}
	if got := faviconRec.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/png") {
		t.Fatalf("expected png content type, got %q", got)
	}
}

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

func TestConfigIncludesCurrentAndLatestCompletedScan(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	cfg.ScanProfile = scancontrol.ProfileThroughput
	cfg.ScanProgressInterval = 125 * time.Millisecond

	st := newTestStore(t, dataDir)
	completedID := createCompletedScanWithNodes(t, st, root, []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 12, MtimeUnix: time.Now().Unix()},
	})
	currentID, err := st.CreateScanRun(context.Background(), root)
	if err != nil {
		t.Fatalf("create running scan: %v", err)
	}
	if err := st.MarkScanRunning(context.Background(), currentID, time.Now().UTC()); err != nil {
		t.Fatalf("mark running: %v", err)
	}

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

	var payload struct {
		ScanProfile            string         `json:"scan_profile"`
		ScanProgressIntervalMS int            `json:"scan_progress_interval_ms"`
		CurrentScan            *store.ScanRun `json:"current_scan"`
		LatestCompletedScan    *store.ScanRun `json:"latest_completed_scan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if payload.ScanProfile != string(scancontrol.ProfileThroughput) {
		t.Fatalf("expected throughput profile, got %q", payload.ScanProfile)
	}
	if payload.ScanProgressIntervalMS != 125 {
		t.Fatalf("expected progress interval 125ms, got %d", payload.ScanProgressIntervalMS)
	}
	if payload.CurrentScan == nil || payload.CurrentScan.ID != currentID || payload.CurrentScan.Status != "running" {
		t.Fatalf("unexpected current scan: %+v", payload.CurrentScan)
	}
	if payload.LatestCompletedScan == nil || payload.LatestCompletedScan.ID != completedID || payload.LatestCompletedScan.Status != "completed" {
		t.Fatalf("unexpected latest completed scan: %+v", payload.LatestCompletedScan)
	}
}

func TestScansCollectionRejectsGet(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)
	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestExploreEndpointReturnsSummaryAndItems(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)
	scanID := createCompletedScanWithNodes(t, st, root, []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 30, MtimeUnix: 1},
		{Path: filepath.Join(root, "alpha"), ParentPath: root, Name: "alpha", Kind: "dir", SizeBytes: 20, MtimeUnix: 1},
		{Path: filepath.Join(root, "beta.log"), ParentPath: root, Name: "beta.log", Kind: "file", SizeBytes: 10, MtimeUnix: 1},
	})

	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+strconv.FormatInt(scanID, 10)+"/explore?path="+url.QueryEscape(root), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Summary struct {
			MatchingItemCount int64 `json:"matching_item_count"`
		} `json:"summary"`
		Items []store.Node `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if payload.Summary.MatchingItemCount != 2 {
		t.Fatalf("expected 2 matching items, got %d", payload.Summary.MatchingItemCount)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(payload.Items))
	}
}

func TestScanLifecycleFromHTTPToExplore(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	dirPath := filepath.Join(root, "nested")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.bin"), []byte("abc"), 0o644); err != nil {
		t.Fatalf("create root fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "nested.bin"), []byte("12345"), 0o644); err != nil {
		t.Fatalf("create nested fixture: %v", err)
	}

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)
	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/scans", nil)
	startRec := httptest.NewRecorder()
	mux.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start scan: expected 202, got %d: %s", startRec.Code, startRec.Body.String())
	}

	var started struct {
		ScanID int64 `json:"scan_id"`
	}
	if err := json.Unmarshal(startRec.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if started.ScanID == 0 {
		t.Fatal("start response did not include a scan id")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+strconv.FormatInt(started.ScanID, 10), nil)
		statusRec := httptest.NewRecorder()
		mux.ServeHTTP(statusRec, statusReq)
		if statusRec.Code != http.StatusOK {
			t.Fatalf("get scan status: expected 200, got %d: %s", statusRec.Code, statusRec.Body.String())
		}

		var run store.ScanRun
		if err := json.Unmarshal(statusRec.Body.Bytes(), &run); err != nil {
			t.Fatalf("decode scan status: %v", err)
		}
		if run.Status == "completed" {
			break
		}
		if run.Status == "failed" {
			t.Fatalf("scan failed: %s", run.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not complete before timeout; last status=%q", run.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	explorePath := "/api/v1/scans/" + strconv.FormatInt(started.ScanID, 10) + "/explore?path=" + url.QueryEscape(root)
	exploreReq := httptest.NewRequest(http.MethodGet, explorePath, nil)
	exploreRec := httptest.NewRecorder()
	mux.ServeHTTP(exploreRec, exploreReq)
	if exploreRec.Code != http.StatusOK {
		t.Fatalf("explore completed scan: expected 200, got %d: %s", exploreRec.Code, exploreRec.Body.String())
	}

	var explored struct {
		Summary struct {
			TotalBytes        int64 `json:"total_bytes"`
			MatchingItemCount int64 `json:"matching_item_count"`
		} `json:"summary"`
		Items []store.Node `json:"items"`
	}
	if err := json.Unmarshal(exploreRec.Body.Bytes(), &explored); err != nil {
		t.Fatalf("decode explore response: %v", err)
	}
	if explored.Summary.TotalBytes != 8 || explored.Summary.MatchingItemCount != 2 {
		t.Fatalf("unexpected explore summary: %+v", explored.Summary)
	}

	sizes := make(map[string]int64, len(explored.Items))
	for _, item := range explored.Items {
		sizes[item.Name] = item.SizeBytes
	}
	if sizes["root.bin"] != 3 || sizes["nested"] != 5 {
		t.Fatalf("unexpected scanned items: %+v", explored.Items)
	}
}

func TestDeleteScanEndpointRemoved(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)
	scanID := createCompletedScanWithNodes(t, st, root, []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 0, MtimeUnix: 1},
	})

	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/scans/"+strconv.FormatInt(scanID, 10), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDiffEndpointRemoved(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)
	scanID := createCompletedScanWithNodes(t, st, root, []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 0, MtimeUnix: 1},
	})

	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+strconv.FormatInt(scanID, 10)+"/diff?base_scan_id=1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHistoryRedirectsToExplore(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)
	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/history", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "/" {
		t.Fatalf("expected redirect to /, got %q", location)
	}
}

func testConfig(root, dataDir string) config.Config {
	return config.Config{
		AnalyzeRoot:          root,
		DataDir:              dataDir,
		ScanProfile:          scancontrol.ProfileFixed,
		ScanProgressInterval: 25 * time.Millisecond,
		MaxChildrenPerQuery:  100,
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

	writer, err := st.BeginSnapshot(context.Background(), scanID)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	if err := writer.Write(context.Background(), nodes); err != nil {
		_ = writer.Discard()
		t.Fatalf("insert nodes: %v", err)
	}
	if err := writer.Publish(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := st.CompleteScan(context.Background(), scanID, "completed", time.Now().UTC(), 0, int64(len(nodes)), 0, ""); err != nil {
		t.Fatalf("complete scan: %v", err)
	}
	return scanID
}
