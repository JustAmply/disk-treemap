package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFromEnvRequiresAnalyzeRoot(t *testing.T) {
	t.Setenv("ANALYZE_ROOT", "")
	_, err := LoadFromEnv()
	if err == nil {
		t.Fatalf("expected error when ANALYZE_ROOT is missing")
	}
}

func TestLoadFromEnvParsesOptionalValues(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()

	t.Setenv("ANALYZE_ROOT", root)
	t.Setenv("LISTEN_ADDR", ":9090")
	t.Setenv("DATA_DIR", data)
	t.Setenv("SCAN_AUTOTUNE", "false")
	t.Setenv("SCAN_MIN_CONCURRENCY", "3")
	t.Setenv("SCAN_MAX_CONCURRENCY", "8")
	t.Setenv("SCAN_WRITE_BATCH_SIZE", "1024")
	t.Setenv("SCAN_MIN_WRITE_BATCH_SIZE", "512")
	t.Setenv("SCAN_MAX_WRITE_BATCH_SIZE", "4096")
	t.Setenv("SCAN_PROGRESS_INTERVAL_MS", "350")
	t.Setenv("SCAN_TIMEOUT", "15")
	t.Setenv("MAX_CHILDREN_PER_QUERY", "321")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.AnalyzeRoot != root {
		t.Fatalf("unexpected root: %q", cfg.AnalyzeRoot)
	}
	if cfg.ListenAddr != ":9090" {
		t.Fatalf("unexpected listen addr: %q", cfg.ListenAddr)
	}
	if cfg.DataDir != data {
		t.Fatalf("unexpected data dir: %q", cfg.DataDir)
	}
	if cfg.ScanAutotune {
		t.Fatalf("expected autotune to be disabled")
	}
	if cfg.ScanMinConcurrency != 3 {
		t.Fatalf("unexpected min concurrency: %d", cfg.ScanMinConcurrency)
	}
	if cfg.ScanMaxConcurrency != 8 {
		t.Fatalf("unexpected concurrency: %d", cfg.ScanMaxConcurrency)
	}
	if cfg.ScanWriteBatchSize != 1024 {
		t.Fatalf("unexpected write batch size: %d", cfg.ScanWriteBatchSize)
	}
	if cfg.ScanMinWriteBatch != 512 {
		t.Fatalf("unexpected min write batch size: %d", cfg.ScanMinWriteBatch)
	}
	if cfg.ScanMaxWriteBatch != 4096 {
		t.Fatalf("unexpected max write batch size: %d", cfg.ScanMaxWriteBatch)
	}
	if cfg.ScanProgressInterval != 350*time.Millisecond {
		t.Fatalf("unexpected progress interval: %v", cfg.ScanProgressInterval)
	}
	if int(cfg.ScanTimeout.Seconds()) != 15 {
		t.Fatalf("unexpected timeout: %v", cfg.ScanTimeout)
	}
	if cfg.MaxChildrenPerQuery != 321 {
		t.Fatalf("unexpected max children: %d", cfg.MaxChildrenPerQuery)
	}
	if got, want := cfg.DatabasePath(), filepath.Join(data, "scan.db"); got != want {
		t.Fatalf("unexpected database path: got %q want %q", got, want)
	}
}

func TestLoadFromEnvNormalizesSmallValues(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ANALYZE_ROOT", root)
	t.Setenv("SCAN_MIN_CONCURRENCY", "0")
	t.Setenv("SCAN_MAX_CONCURRENCY", "0")
	t.Setenv("SCAN_WRITE_BATCH_SIZE", "0")
	t.Setenv("SCAN_MIN_WRITE_BATCH_SIZE", "0")
	t.Setenv("SCAN_MAX_WRITE_BATCH_SIZE", "0")
	t.Setenv("SCAN_PROGRESS_INTERVAL_MS", "1")
	t.Setenv("MAX_CHILDREN_PER_QUERY", "0")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.ScanMinConcurrency != 1 {
		t.Fatalf("expected min concurrency to floor at 1, got %d", cfg.ScanMinConcurrency)
	}
	if cfg.ScanMaxConcurrency != 1 {
		t.Fatalf("expected max concurrency to floor at min, got %d", cfg.ScanMaxConcurrency)
	}
	if cfg.ScanWriteBatchSize != 1 {
		t.Fatalf("expected write batch size to floor at 1, got %d", cfg.ScanWriteBatchSize)
	}
	if cfg.ScanMinWriteBatch != 1 {
		t.Fatalf("expected min write batch size to floor at 1, got %d", cfg.ScanMinWriteBatch)
	}
	if cfg.ScanMaxWriteBatch != 1 {
		t.Fatalf("expected max write batch size to clamp to min, got %d", cfg.ScanMaxWriteBatch)
	}
	if cfg.ScanProgressInterval != 10*time.Millisecond {
		t.Fatalf("expected progress interval floor at 10ms, got %v", cfg.ScanProgressInterval)
	}
	if cfg.MaxChildrenPerQuery != defaultMaxChildrenPerQuery {
		t.Fatalf("expected default max children, got %d", cfg.MaxChildrenPerQuery)
	}
}

func TestLoadFromEnvDefaultsAutotuneOn(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ANALYZE_ROOT", root)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if !cfg.ScanAutotune {
		t.Fatalf("expected autotune enabled by default")
	}
	if cfg.ScanMinConcurrency != 1 {
		t.Fatalf("unexpected min concurrency: %d", cfg.ScanMinConcurrency)
	}
	if cfg.ScanMaxConcurrency < 4 || cfg.ScanMaxConcurrency > 64 {
		t.Fatalf("expected bounded automatic max concurrency, got %d", cfg.ScanMaxConcurrency)
	}
	if cfg.ScanMinWriteBatch != 1 {
		t.Fatalf("unexpected min write batch size: %d", cfg.ScanMinWriteBatch)
	}
	if cfg.ScanMaxWriteBatch != 32768 {
		t.Fatalf("unexpected max write batch size: %d", cfg.ScanMaxWriteBatch)
	}
}

func TestLoadFromEnvClampsMaxToMinConcurrency(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ANALYZE_ROOT", root)
	t.Setenv("SCAN_MIN_CONCURRENCY", "6")
	t.Setenv("SCAN_MAX_CONCURRENCY", "2")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.ScanMinConcurrency != 6 {
		t.Fatalf("unexpected min concurrency: %d", cfg.ScanMinConcurrency)
	}
	if cfg.ScanMaxConcurrency != 6 {
		t.Fatalf("expected max concurrency to clamp to min, got %d", cfg.ScanMaxConcurrency)
	}
}

func TestLoadFromEnvClampsWriteBatchBounds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ANALYZE_ROOT", root)
	t.Setenv("SCAN_WRITE_BATCH_SIZE", "99")
	t.Setenv("SCAN_MIN_WRITE_BATCH_SIZE", "128")
	t.Setenv("SCAN_MAX_WRITE_BATCH_SIZE", "64")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.ScanMinWriteBatch != 128 {
		t.Fatalf("unexpected min write batch size: %d", cfg.ScanMinWriteBatch)
	}
	if cfg.ScanMaxWriteBatch != 128 {
		t.Fatalf("expected max write batch size to clamp to min, got %d", cfg.ScanMaxWriteBatch)
	}
	if cfg.ScanWriteBatchSize != 128 {
		t.Fatalf("expected initial write batch size to clamp to min, got %d", cfg.ScanWriteBatchSize)
	}
}
