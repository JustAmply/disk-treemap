package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/justamply/disk-treemap/internal/app"
	"github.com/justamply/disk-treemap/internal/config"
	"github.com/justamply/disk-treemap/internal/pathutil"
	"github.com/justamply/disk-treemap/internal/store"
)

type Handler struct {
	svc        *app.Service
	cfg        config.Config
	staticFS   http.Handler
	staticRoot string
}

func NewHandler(svc *app.Service, cfg config.Config, staticDir string) *Handler {
	return &Handler{
		svc:        svc,
		cfg:        cfg,
		staticFS:   http.FileServer(http.Dir(staticDir)),
		staticRoot: staticDir,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/health", h.handleHealth)
	mux.HandleFunc("/api/v1/config", h.handleConfig)
	mux.HandleFunc("/api/v1/scans", h.handleScans)
	mux.HandleFunc("/api/v1/scans/", h.handleScanRoutes)
	mux.HandleFunc("/", h.handleStatic)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	latest, err := h.svc.GetLatestScanRun(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"analyze_root":              h.cfg.AnalyzeRoot,
		"listen_addr":               h.cfg.ListenAddr,
		"data_dir":                  h.cfg.DataDir,
		"scan_max_concurrency":      h.cfg.ScanMaxConcurrency,
		"scan_write_batch_size":     h.cfg.ScanWriteBatchSize,
		"scan_progress_interval_ms": int(h.cfg.ScanProgressInterval / time.Millisecond),
		"scan_timeout_seconds":      int(h.cfg.ScanTimeout.Seconds()),
		"max_children_per_query":    h.cfg.MaxChildrenPerQuery,
		"scan_history_max_runs":     h.cfg.ScanHistoryMaxRuns,
		"latest_scan":               latest,
	})
}

func (h *Handler) handleScans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleStartScan(w, r)
	case http.MethodGet:
		h.handleListScans(w, r)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) handleStartScan(w http.ResponseWriter, r *http.Request) {
	scanID, err := h.svc.StartScan(r.Context())
	if err != nil {
		if errors.Is(err, app.ErrScanRunning) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"scan_id": scanID})
}

func (h *Handler) handleListScans(w http.ResponseWriter, r *http.Request) {
	limit, err := parseIntQuery(r, "limit", 50)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))

	runs, err := h.svc.ListScans(r.Context(), limit, status)
	if err != nil {
		h.handleDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": runs})
}

func (h *Handler) handleScanRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/scans/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	parts := strings.Split(rest, "/")
	scanID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid scan id")
		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.handleGetScan(w, r, scanID)
		case http.MethodDelete:
			h.handleDeleteScan(w, r, scanID)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if len(parts) != 2 {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	switch parts[1] {
	case "children":
		h.handleGetChildren(w, r, scanID)
	case "largest":
		h.handleGetLargest(w, r, scanID)
	case "diff":
		h.handleGetDiff(w, r, scanID)
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) handleGetScan(w http.ResponseWriter, r *http.Request, scanID int64) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	run, err := h.svc.GetScanRun(r.Context(), scanID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "scan not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) handleDeleteScan(w http.ResponseWriter, r *http.Request, scanID int64) {
	if r.Method != http.MethodDelete {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if err := h.svc.DeleteScan(r.Context(), scanID); err != nil {
		h.handleDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleGetChildren(w http.ResponseWriter, r *http.Request, scanID int64) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit, err := parseIntQuery(r, "limit", h.cfg.MaxChildrenPerQuery)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	minSize, err := parseInt64Query(r, "min_size", 0)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.svc.GetChildren(r.Context(), scanID, r.URL.Query().Get("path"), app.NodeQueryOptions{
		Limit:   limit,
		Query:   strings.TrimSpace(r.URL.Query().Get("q")),
		Kind:    strings.TrimSpace(r.URL.Query().Get("type")),
		MinSize: minSize,
		Sort:    strings.TrimSpace(r.URL.Query().Get("sort")),
	})
	if err != nil {
		h.handleDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleGetLargest(w http.ResponseWriter, r *http.Request, scanID int64) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit, err := parseIntQuery(r, "limit", 100)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	minSize, err := parseInt64Query(r, "min_size", 0)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.svc.GetLargest(r.Context(), scanID, r.URL.Query().Get("path"), app.NodeQueryOptions{
		Limit:   limit,
		Query:   strings.TrimSpace(r.URL.Query().Get("q")),
		Kind:    strings.TrimSpace(r.URL.Query().Get("type")),
		MinSize: minSize,
		Sort:    strings.TrimSpace(r.URL.Query().Get("sort")),
	})
	if err != nil {
		h.handleDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleGetDiff(w http.ResponseWriter, r *http.Request, targetScanID int64) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	baseScanID, err := parseInt64Query(r, "base_scan_id", 0)
	if err != nil || baseScanID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "base_scan_id is required")
		return
	}

	limit, err := parseIntQuery(r, "limit", 100)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.svc.GetDirectoryDiff(r.Context(), targetScanID, baseScanID, r.URL.Query().Get("path"), limit)
	if err != nil {
		h.handleDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pathutil.ErrPathNotAbsolute), errors.Is(err, pathutil.ErrPathOutsideRoot):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, app.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, app.ErrScanRunning):
		writeJSONError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not found")
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *Handler) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	if r.URL.Path == "/" {
		http.ServeFile(w, r, filepath.Join(h.staticRoot, "index.html"))
		return
	}

	h.staticFS.ServeHTTP(w, r)
}

func parseIntQuery(r *http.Request, key string, defaultValue int) (int, error) {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return 0, errors.New("invalid " + key)
	}
	return parsed, nil
}

func parseInt64Query(r *http.Request, key string, defaultValue int64) (int64, error) {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, errors.New("invalid " + key)
	}
	return parsed, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
