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
		"latest_scan":               latest,
	})
}

func (h *Handler) handleScans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

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
		h.handleGetScan(w, r, scanID)
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

func (h *Handler) handleGetChildren(w http.ResponseWriter, r *http.Request, scanID int64) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit := h.cfg.MaxChildrenPerQuery
	if v := r.URL.Query().Get("limit"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	resp, err := h.svc.GetChildren(r.Context(), scanID, r.URL.Query().Get("path"), limit)
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

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	resp, err := h.svc.GetLargest(r.Context(), scanID, r.URL.Query().Get("path"), limit)
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
