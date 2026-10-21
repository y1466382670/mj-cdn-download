package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/y1466382670/mj-cdn-download/internal/api"
	"github.com/y1466382670/mj-cdn-download/internal/config"
	"github.com/y1466382670/mj-cdn-download/internal/download"
	"github.com/y1466382670/mj-cdn-download/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("创建数据目录失败: %v", err)
	}

	svc := download.New(download.Options{
		DataDir:     cfg.DataDir,
		Timeout:     cfg.Timeout,
		MaxRetry:    cfg.MaxRetry,
		MaxInflight: cfg.MaxInflight,
		Headless:    cfg.Headless,
		ChromeBin:   cfg.ChromeBin,
		Solve:       cfg.Solve,
	})
	defer svc.Close()

	apiSrv := api.New(cfg, svc)
	if cfg.S3.Enabled {
		store, err := storage.NewS3(storage.Options{
			Endpoint:  cfg.S3.Endpoint,
			Region:    cfg.S3.Region,
			Bucket:    cfg.S3.Bucket,
			AccessKey: cfg.S3.AccessKey,
			SecretKey: cfg.S3.SecretKey,
			PathStyle: cfg.S3.PathStyle,
		})
		if err != nil {
			log.Fatalf("对象存储初始化失败: %v", err)
		}
		apiSrv.UseUploader(store)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       time.Minute,
	}

	go func() {
		log.Printf("mj-cdn-download 监听 %s", cfg.Addr)
		log.Printf("数据目录 %s", cfg.DataDir)
		log.Printf("过挑战 %s，存储 %s", onOff(cfg.Solve), storageName(cfg))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("服务退出: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("关闭失败: %v", err)
	}
}

func onOff(v bool) string {
	if v {
		return "开"
	}
	return "关"
}

func storageName(cfg config.Config) string {
	switch {
	case cfg.S3.Enabled && cfg.CDNBaseURL != "":
		return "对象存储 " + cfg.S3.Bucket + "，CDN " + cfg.CDNBaseURL
	case cfg.S3.Enabled:
		return "对象存储 " + cfg.S3.Bucket
	case cfg.CDNBaseURL != "":
		return "本地文件，CDN " + cfg.CDNBaseURL
	default:
		return "本地文件"
	}
}
