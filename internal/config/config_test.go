package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justamply/disk-treemap/internal/scancontrol"
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
	t.Setenv("SCAN_PROFILE", "throughput")
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
	if cfg.ScanProfile != scancontrol.ProfileThroughput {
		t.Fatalf("unexpected scan profile: %q", cfg.ScanProfile)
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

func TestLoadFromEnvDefaultsToBalancedProfile(t *testing.T) {
	t.Setenv("ANALYZE_ROOT", t.TempDir())

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ScanProfile != scancontrol.ProfileBalanced {
		t.Fatalf("expected balanced profile, got %q", cfg.ScanProfile)
	}
}

func TestLoadFromEnvRejectsUnknownProfile(t *testing.T) {
	t.Setenv("ANALYZE_ROOT", t.TempDir())
	t.Setenv("SCAN_PROFILE", "turbo")

	_, err := LoadFromEnv()
	if err == nil || !strings.Contains(err.Error(), "invalid SCAN_PROFILE") {
		t.Fatalf("expected invalid profile error, got %v", err)
	}
}

func TestLoadFromEnvRejectsRemovedTuningVariables(t *testing.T) {
	for _, name := range removedTuningEnv {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ANALYZE_ROOT", t.TempDir())
			t.Setenv(name, "1")

			_, err := LoadFromEnv()
			if err == nil || !strings.Contains(err.Error(), name+" is no longer supported") {
				t.Fatalf("expected migration error for %s, got %v", name, err)
			}
		})
	}
}

func TestLoadFromEnvNormalizesSmallValues(t *testing.T) {
	t.Setenv("ANALYZE_ROOT", t.TempDir())
	t.Setenv("SCAN_PROGRESS_INTERVAL_MS", "1")
	t.Setenv("MAX_CHILDREN_PER_QUERY", "0")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ScanProgressInterval != 10*time.Millisecond {
		t.Fatalf("expected progress interval floor at 10ms, got %v", cfg.ScanProgressInterval)
	}
	if cfg.MaxChildrenPerQuery != defaultMaxChildrenPerQuery {
		t.Fatalf("expected default max children, got %d", cfg.MaxChildrenPerQuery)
	}
}
