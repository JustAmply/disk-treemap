package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	defaultListenAddr          = ":8080"
	defaultDataDir             = "/data"
	defaultScanMaxConcurrency  = 4
	defaultScanWriteBatchSize  = 512
	defaultScanProgressMS      = 200
	minScanProgressMS          = 10
	defaultMaxChildrenPerQuery = 500
	defaultScanHistoryMaxRuns  = 50
)

type Config struct {
	AnalyzeRoot          string
	ListenAddr           string
	DataDir              string
	ScanMaxConcurrency   int
	ScanWriteBatchSize   int
	ScanProgressInterval time.Duration
	ScanTimeout          time.Duration
	MaxChildrenPerQuery  int
	ScanHistoryMaxRuns   int
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		AnalyzeRoot:          os.Getenv("ANALYZE_ROOT"),
		ListenAddr:           getenvDefault("LISTEN_ADDR", defaultListenAddr),
		DataDir:              getenvDefault("DATA_DIR", defaultDataDir),
		ScanMaxConcurrency:   defaultScanMaxConcurrency,
		ScanWriteBatchSize:   defaultScanWriteBatchSize,
		ScanProgressInterval: time.Duration(defaultScanProgressMS) * time.Millisecond,
		MaxChildrenPerQuery:  defaultMaxChildrenPerQuery,
		ScanHistoryMaxRuns:   defaultScanHistoryMaxRuns,
	}

	if cfg.AnalyzeRoot == "" {
		return Config{}, errors.New("ANALYZE_ROOT is required")
	}
	if !filepath.IsAbs(cfg.AnalyzeRoot) {
		return Config{}, fmt.Errorf("ANALYZE_ROOT must be absolute: %q", cfg.AnalyzeRoot)
	}

	var err error
	cfg.ScanMaxConcurrency, err = parseIntEnv("SCAN_MAX_CONCURRENCY", defaultScanMaxConcurrency)
	if err != nil {
		return Config{}, err
	}
	if cfg.ScanMaxConcurrency < 1 {
		cfg.ScanMaxConcurrency = 1
	}

	cfg.ScanWriteBatchSize, err = parseIntEnv("SCAN_WRITE_BATCH_SIZE", defaultScanWriteBatchSize)
	if err != nil {
		return Config{}, err
	}
	if cfg.ScanWriteBatchSize < 1 {
		cfg.ScanWriteBatchSize = 1
	}

	progressMS, err := parseIntEnv("SCAN_PROGRESS_INTERVAL_MS", defaultScanProgressMS)
	if err != nil {
		return Config{}, err
	}
	if progressMS < minScanProgressMS {
		progressMS = minScanProgressMS
	}
	cfg.ScanProgressInterval = time.Duration(progressMS) * time.Millisecond

	timeoutSeconds, err := parseIntEnv("SCAN_TIMEOUT", 0)
	if err != nil {
		return Config{}, err
	}
	if timeoutSeconds < 0 {
		return Config{}, fmt.Errorf("SCAN_TIMEOUT must be >= 0")
	}
	cfg.ScanTimeout = time.Duration(timeoutSeconds) * time.Second

	cfg.MaxChildrenPerQuery, err = parseIntEnv("MAX_CHILDREN_PER_QUERY", defaultMaxChildrenPerQuery)
	if err != nil {
		return Config{}, err
	}
	if cfg.MaxChildrenPerQuery < 1 {
		cfg.MaxChildrenPerQuery = defaultMaxChildrenPerQuery
	}

	cfg.ScanHistoryMaxRuns, err = parseIntEnv("SCAN_HISTORY_MAX_RUNS", defaultScanHistoryMaxRuns)
	if err != nil {
		return Config{}, err
	}
	if cfg.ScanHistoryMaxRuns < 1 {
		cfg.ScanHistoryMaxRuns = defaultScanHistoryMaxRuns
	}

	if err := validateAnalyzeRoot(cfg.AnalyzeRoot); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "scan.db")
}

func parseIntEnv(name string, defaultValue int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return defaultValue, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	return n, nil
}

func getenvDefault(name, defaultValue string) string {
	v := os.Getenv(name)
	if v == "" {
		return defaultValue
	}
	return v
}

func validateAnalyzeRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("failed to access ANALYZE_ROOT %q: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("ANALYZE_ROOT must be a directory: %q", root)
	}

	d, err := os.Open(root)
	if err != nil {
		return fmt.Errorf("ANALYZE_ROOT is not readable: %w", err)
	}
	defer d.Close()

	_, err = d.Readdirnames(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("ANALYZE_ROOT cannot be listed: %w", err)
	}
	return nil
}
