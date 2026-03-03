package config

import (
	"path/filepath"
	"testing"
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
	t.Setenv("SCAN_MAX_CONCURRENCY", "8")
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
	if cfg.ScanMaxConcurrency != 8 {
		t.Fatalf("unexpected concurrency: %d", cfg.ScanMaxConcurrency)
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
	t.Setenv("SCAN_MAX_CONCURRENCY", "0")
	t.Setenv("MAX_CHILDREN_PER_QUERY", "0")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.ScanMaxConcurrency != 1 {
		t.Fatalf("expected concurrency to floor at 1, got %d", cfg.ScanMaxConcurrency)
	}
	if cfg.MaxChildrenPerQuery != defaultMaxChildrenPerQuery {
		t.Fatalf("expected default max children, got %d", cfg.MaxChildrenPerQuery)
	}
}
