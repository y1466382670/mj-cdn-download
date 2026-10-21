package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{
		"ADDR", "PORT", "DATA_DIR", "CDN_BASE_URL",
		"TIMEOUT_SECONDS", "BUDGET_SECONDS", "MAX_RETRY", "MAX_INFLIGHT",
		"SOLVE", "HEADLESS", "CHROME_BIN", "STORAGE",
		"S3_ENDPOINT", "S3_REGION", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_PATH_STYLE",
	} {
		t.Setenv(key, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("addr %s", cfg.Addr)
	}
	if !filepath.IsAbs(cfg.DataDir) || filepath.Base(cfg.DataDir) != "data" {
		t.Fatalf("data dir %s", cfg.DataDir)
	}
	if cfg.Timeout != 30*time.Second || cfg.Budget != 90*time.Second {
		t.Fatalf("timeouts %s %s", cfg.Timeout, cfg.Budget)
	}
	if cfg.MaxRetry != 3 || cfg.MaxInflight != 32 || !cfg.Solve || cfg.Headless != "new" {
		t.Fatalf("%+v", cfg)
	}
	if cfg.CDNBaseURL != "" || cfg.S3.Enabled {
		t.Fatalf("storage %+v cdn %s", cfg.S3, cfg.CDNBaseURL)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("ADDR", "127.0.0.1:9091")
	t.Setenv("CDN_BASE_URL", "https://img.example.com/files/")
	t.Setenv("STORAGE", "s3")
	t.Setenv("S3_ENDPOINT", "oss-cn-hangzhou.aliyuncs.com")
	t.Setenv("S3_BUCKET", "demo")
	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")
	t.Setenv("S3_REGION", "oss-cn-hangzhou")
	t.Setenv("S3_PATH_STYLE", "false")
	t.Setenv("SOLVE", "off")
	t.Setenv("HEADLESS", "old")
	t.Setenv("MAX_RETRY", "5")
	t.Setenv("TIMEOUT_SECONDS", "12")
	t.Setenv("BUDGET_SECONDS", "40")
	t.Setenv("MAX_INFLIGHT", "4")
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:9091" {
		t.Fatalf("addr %s", cfg.Addr)
	}
	if cfg.CDNBaseURL != "https://img.example.com/files" {
		t.Fatalf("base %s", cfg.CDNBaseURL)
	}
	if !cfg.S3.Enabled || cfg.S3.Endpoint != "https://oss-cn-hangzhou.aliyuncs.com" || cfg.S3.PathStyle || cfg.S3.Bucket != "demo" {
		t.Fatalf("%+v", cfg.S3)
	}
	if cfg.Solve || cfg.Headless != "old" {
		t.Fatalf("%+v", cfg)
	}
	if cfg.MaxRetry != 5 || cfg.Timeout != 12*time.Second || cfg.Budget != 40*time.Second || cfg.MaxInflight != 4 {
		t.Fatalf("%+v", cfg)
	}
	if cfg.DataDir != dir {
		t.Fatalf("data %s", cfg.DataDir)
	}
}

func TestLoadRejectsBadInt(t *testing.T) {
	t.Setenv("MAX_RETRY", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadS3(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("S3_ENDPOINT", "https://abc.r2.cloudflarestorage.com")
	t.Setenv("S3_BUCKET", "images")
	t.Setenv("S3_ACCESS_KEY", "ak")
	if _, err := Load(); err == nil {
		t.Fatal("incomplete object storage should fail")
	}

	t.Setenv("S3_SECRET_KEY", "sk")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.S3.Enabled || !cfg.S3.PathStyle || cfg.S3.Region != "auto" {
		t.Fatalf("%+v", cfg.S3)
	}

	t.Setenv("STORAGE", "local")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.S3.Enabled {
		t.Fatal("STORAGE=local should ignore object storage")
	}
}

func clearStorageEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"STORAGE", "CDN_BASE_URL", "S3_ENDPOINT", "S3_REGION", "S3_BUCKET",
		"S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_PATH_STYLE",
	} {
		t.Setenv(key, "")
	}
}
