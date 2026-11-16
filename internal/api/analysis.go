package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/justamply/disk-treemap/internal/app"
	"github.com/justamply/disk-treemap/internal/pathutil"
	"github.com/justamply/disk-treemap/internal/store"
)

func (h *Handler) handleAnalysis(w http.ResponseWriter, r *http.Request, scanID int64, action string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	request, err := parseFolderViewRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch action {
	case "diff":
		base, err := parseInt64Query(r, "base", 0)
		if err != nil || base <= 0 {
			writeJSONError(w, http.StatusBadRequest, "base must be a positive scan ID")
			return
		}
		result, err := h.svc.CompareSnapshots(r.Context(), base, scanID, request)
		if err != nil {
			h.handleDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "extensions":
		items, err := h.svc.SummarizeExtensions(r.Context(), scanID, request)
		if err != nil {
			h.handleDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case "export":
		h.handleExport(w, r, scanID, request)
	}
}

func (h *Handler) handleExport(w http.ResponseWriter, r *http.Request, scanID int64, request app.FolderViewRequest) {
	normalized, err := pathutil.NormalizeWithinRoot(h.cfg.AnalyzeRoot, request.Path)
	if err != nil {
		h.handleDomainError(w, err)
		return
	}
	request.Path = normalized
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "folder"
	}
	if (format != "csv" && format != "json") || (scope != "folder" && scope != "recursive") {
		writeJSONError(w, http.StatusBadRequest, "unsupported export format or scope")
		return
	}
	// Spool to disk so query failures return a proper error without buffering an entire tree in memory.
	f, err := os.CreateTemp("", "disk-treemap-export-*")
	if err != nil {
		h.handleDomainError(w, err)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	csvWriter := csv.NewWriter(f)
	if format == "csv" {
		err = csvWriter.Write([]string{"scan_id", "scan_finished_at", "path", "type", "size_bytes", "allocated_bytes", "mtime_unix", "incomplete", "excluded", "hardlink"})
	} else {
		_, err = io.WriteString(f, `{"items":[`)
	}
	if err != nil {
		h.handleDomainError(w, err)
		return
	}
	run, err := h.svc.GetScanRun(r.Context(), scanID)
	if err != nil {
		h.handleDomainError(w, err)
		return
	}
	finished := ""
	if run.FinishedAt != nil {
		finished = run.FinishedAt.Format(time.RFC3339Nano)
	}
	first := true
	_, err = h.svc.ExportNodes(r.Context(), scanID, request, scope == "recursive", func(n store.Node) error {
		if format == "csv" {
			allocated := ""
			if n.AllocatedBytes != nil {
				allocated = strconv.FormatInt(*n.AllocatedBytes, 10)
			}
			return csvWriter.Write([]string{strconv.FormatInt(scanID, 10), finished, csvCell(n.Path), n.Kind, strconv.FormatInt(n.SizeBytes, 10), allocated, strconv.FormatInt(n.MtimeUnix, 10), strconv.FormatBool(n.Incomplete), strconv.FormatBool(n.Excluded), strconv.FormatBool(n.Hardlink)})
		}
		if !first {
			if _, err := io.WriteString(f, ","); err != nil {
				return err
			}
		}
		first = false
		return json.NewEncoder(f).Encode(n)
	})
	if err == nil && format == "csv" {
		csvWriter.Flush()
		err = csvWriter.Error()
	}
	if err == nil && format == "json" {
		_, err = fmt.Fprintf(f, `],"scan_id":%d,"scan_finished_at":%q}`, scanID, finished)
	}
	if err != nil {
		h.handleDomainError(w, err)
		return
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		h.handleDomainError(w, err)
		return
	}
	contentType := "text/csv; charset=utf-8"
	if format == "json" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="scan-%d.%s"`, scanID, format))
	_, _ = io.Copy(w, f)
}

// Spreadsheet applications interpret formula prefixes even in quoted CSV cells.
func csvCell(value string) string {
	if strings.ContainsAny(value[:min(1, len(value))], "=+-@\t\r\n") {
		return "'" + value
	}
	return value
}
