package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config 是进程启动时从环境变量读到的配置。代理不写在这里：
// 每次请求自己带，带了就用，没带就直连。
type Config struct {
	Addr        string
	DataDir     string
	CDNBaseURL  string
	Timeout     time.Duration
	Budget      time.Duration
	MaxRetry    int
	MaxInflight int
	Headless    string
	ChromeBin   string
	Solve       bool
	S3          S3Config
}

// S3Config 是阿里云 OSS、Cloudflare R2，以及其他 S3 兼容存储的连接信息。
type S3Config struct {
	Enabled   bool
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	PathStyle bool
}

func Load() (Config, error) {
	cfg := Config{
		Addr:        ":8080",
		DataDir:     "data",
		Timeout:     30 * time.Second,
		Budget:      90 * time.Second,
		MaxRetry:    3,
		MaxInflight: 32,
		Headless:    "new",
		Solve:       true,
	}

	if v := strings.TrimSpace(os.Getenv("ADDR")); v != "" {
		cfg.Addr = v
	} else if v := strings.TrimSpace(os.Getenv("PORT")); v != "" {
		if strings.Contains(v, ":") {
			cfg.Addr = v
		} else {
			cfg.Addr = ":" + v
		}
	}

	if v := strings.TrimSpace(os.Getenv("DATA_DIR")); v != "" {
		cfg.DataDir = v
	}
	abs, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("DATA_DIR: %w", err)
	}
	cfg.DataDir = abs

	cfg.CDNBaseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("CDN_BASE_URL")), "/")
	cfg.ChromeBin = strings.TrimSpace(os.Getenv("CHROME_BIN"))

	timeout, err := intEnv("TIMEOUT_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	budget, err := intEnv("BUDGET_SECONDS", 90)
	if err != nil {
		return Config{}, err
	}
	retry, err := intEnv("MAX_RETRY", 3)
	if err != nil {
		return Config{}, err
	}
	inflight, err := intEnv("MAX_INFLIGHT", 32)
	if err != nil {
		return Config{}, err
	}
	cfg.Timeout = time.Duration(timeout) * time.Second
	cfg.Budget = time.Duration(budget) * time.Second
	cfg.MaxRetry = retry
	cfg.MaxInflight = inflight

	switch strings.ToLower(strings.TrimSpace(os.Getenv("SOLVE"))) {
	case "", "1", "true", "yes", "on":
		cfg.Solve = true
	case "0", "false", "no", "off":
		cfg.Solve = false
	default:
		return Config{}, fmt.Errorf("SOLVE 只能是 true 或 false")
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("HEADLESS"))) {
	case "", "new":
		cfg.Headless = "new"
	case "old":
		cfg.Headless = "old"
	case "off", "false", "0":
		cfg.Headless = "off"
	default:
		return Config{}, fmt.Errorf("HEADLESS 只能是 new、old 或 off")
	}

	s3cfg, err := loadS3()
	if err != nil {
		return Config{}, err
	}
	cfg.S3 = s3cfg
	return cfg, nil
}

func loadS3() (S3Config, error) {
	endpoint := strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("S3_BUCKET"))
	accessKey := strings.TrimSpace(os.Getenv("S3_ACCESS_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("S3_SECRET_KEY"))
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE")))
	switch mode {
	case "", "local", "s3":
	default:
		return S3Config{}, fmt.Errorf("STORAGE 只能是 local 或 s3")
	}
	if mode == "local" {
		return S3Config{}, nil
	}
	filled := endpoint != "" || bucket != "" || accessKey != "" || secretKey != ""
	if mode == "" && !filled {
		return S3Config{}, nil
	}
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return S3Config{}, fmt.Errorf("对象存储需要同时设置 S3_ENDPOINT、S3_BUCKET、S3_ACCESS_KEY、S3_SECRET_KEY")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return S3Config{}, fmt.Errorf("S3_ENDPOINT 无法解析")
	}
	region := strings.TrimSpace(os.Getenv("S3_REGION"))
	if region == "" {
		region = "auto"
	}
	pathStyle, err := pathStyleEnv(endpoint)
	if err != nil {
		return S3Config{}, err
	}
	return S3Config{
		Enabled:   true,
		Endpoint:  strings.TrimRight(endpoint, "/"),
		Region:    region,
		Bucket:    bucket,
		AccessKey: accessKey,
		SecretKey: secretKey,
		PathStyle: pathStyle,
	}, nil
}

func pathStyleEnv(endpoint string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("S3_PATH_STYLE"))) {
	case "":
		return strings.Contains(strings.ToLower(endpoint), "r2.cloudflarestorage.com"), nil
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("S3_PATH_STYLE 只能是 true 或 false")
	}
}

func intEnv(key string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s 必须是正整数", key)
	}
	return n, nil
}
