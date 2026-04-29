package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

const (
	defaultListenAddr          = ":8080"
	defaultDataDir             = "/data"
	defaultScanAutotune        = true
	defaultScanMinConcurrency  = 1
	defaultScanWriteBatchSize  = 2048
	defaultScanMinWriteBatch   = 1
	defaultScanMaxWriteBatch   = 32768
	defaultScanProgressMS      = 200
	minScanProgressMS          = 10
	defaultMaxChildrenPerQuery = 500
)

type Config struct {
	AnalyzeRoot          string
	ListenAddr           string
	DataDir              string
	ScanAutotune         bool
	ScanMinConcurrency   int
	ScanMaxConcurrency   int
	ScanWriteBatchSize   int
	ScanMinWriteBatch    int
	ScanMaxWriteBatch    int
	ScanProgressInterval time.Duration
	ScanTimeout          time.Duration
	MaxChildrenPerQuery  int
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		AnalyzeRoot:          os.Getenv("ANALYZE_ROOT"),
		ListenAddr:           getenvDefault("LISTEN_ADDR", defaultListenAddr),
		DataDir:              getenvDefault("DATA_DIR", defaultDataDir),
		ScanAutotune:         defaultScanAutotune,
		ScanMinConcurrency:   defaultScanMinConcurrency,
		ScanMaxConcurrency:   defaultScanMaxConcurrency(),
		ScanWriteBatchSize:   defaultScanWriteBatchSize,
		ScanMinWriteBatch:    defaultScanMinWriteBatch,
		ScanMaxWriteBatch:    defaultScanMaxWriteBatch,
		ScanProgressInterval: time.Duration(defaultScanProgressMS) * time.Millisecond,
		MaxChildrenPerQuery:  defaultMaxChildrenPerQuery,
	}

	if cfg.AnalyzeRoot == "" {
		return Config{}, errors.New("ANALYZE_ROOT is required")
	}
	if !filepath.IsAbs(cfg.AnalyzeRoot) {
		return Config{}, fmt.Errorf("ANALYZE_ROOT must be absolute: %q", cfg.AnalyzeRoot)
	}

	var err error
	cfg.ScanAutotune, err = parseBoolEnv("SCAN_AUTOTUNE", defaultScanAutotune)
	if err != nil {
		return Config{}, err
	}

	cfg.ScanMinConcurrency, err = parseIntEnv("SCAN_MIN_CONCURRENCY", defaultScanMinConcurrency)
	if err != nil {
		return Config{}, err
	}
	if cfg.ScanMinConcurrency < 1 {
		cfg.ScanMinConcurrency = 1
	}

	cfg.ScanMaxConcurrency, err = parseIntEnv("SCAN_MAX_CONCURRENCY", defaultScanMaxConcurrency())
	if err != nil {
		return Config{}, err
	}
	if cfg.ScanMaxConcurrency < cfg.ScanMinConcurrency {
		cfg.ScanMaxConcurrency = cfg.ScanMinConcurrency
	}

	cfg.ScanWriteBatchSize, err = parseIntEnv("SCAN_WRITE_BATCH_SIZE", defaultScanWriteBatchSize)
	if err != nil {
		return Config{}, err
	}
	if cfg.ScanWriteBatchSize < 1 {
		cfg.ScanWriteBatchSize = 1
	}

	cfg.ScanMinWriteBatch, err = parseIntEnv("SCAN_MIN_WRITE_BATCH_SIZE", defaultScanMinWriteBatch)
	if err != nil {
		return Config{}, err
	}
	if cfg.ScanMinWriteBatch < 1 {
		cfg.ScanMinWriteBatch = 1
	}

	cfg.ScanMaxWriteBatch, err = parseIntEnv("SCAN_MAX_WRITE_BATCH_SIZE", defaultScanMaxWriteBatch)
	if err != nil {
		return Config{}, err
	}
	if cfg.ScanMaxWriteBatch < cfg.ScanMinWriteBatch {
		cfg.ScanMaxWriteBatch = cfg.ScanMinWriteBatch
	}
	if cfg.ScanWriteBatchSize < cfg.ScanMinWriteBatch {
		cfg.ScanWriteBatchSize = cfg.ScanMinWriteBatch
	}
	if cfg.ScanWriteBatchSize > cfg.ScanMaxWriteBatch {
		cfg.ScanWriteBatchSize = cfg.ScanMaxWriteBatch
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

	if err := validateAnalyzeRoot(cfg.AnalyzeRoot); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func defaultScanMaxConcurrency() int {
	n := runtime.NumCPU() * 4
	if n < 4 {
		n = 4
	}
	if n > 64 {
		n = 64
	}
	return n
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

func parseBoolEnv(name string, defaultValue bool) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return defaultValue, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %w", name, err)
	}
	return b, nil
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
