package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/yanzhiyu/mj-cdn-download/internal/config"
	"github.com/yanzhiyu/mj-cdn-download/internal/download"
)

// Downloader 下载一张 cdn.midjourney.com 图片。
type Downloader interface {
	Download(ctx context.Context, rawURL string, proxy download.Spec) (download.Result, error)
}

// Uploader 把已经落在本地的图片交给对象存储，并返回对象自己的地址。
type Uploader interface {
	Upload(ctx context.Context, localPath, key string) (string, error)
}

type Server struct {
	cfg      config.Config
	dl       Downloader
	files    http.Handler
	uploader Uploader
}

func New(cfg config.Config, dl Downloader) *Server {
	return &Server{
		cfg:   cfg,
		dl:    dl,
		files: fileServer(cfg.DataDir),
	}
}

// UseUploader 在配置了对象存储时接入。不调用则只保留本地文件。
func (s *Server) UseUploader(u Uploader) {
	s.uploader = u
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /download", s.download)
	mux.Handle("GET /files/", s.files)
	return recoverMW(mux)
}

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, indexHTML)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	raw, spec, err := decodeRequest(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		msg := err.Error()
		writeErr(w, msg)
		log.Printf("[mj] %s code=500 cost=%.2fs msg=%s", r.URL.Path, time.Since(start).Seconds(), msg)
		return
	}

	ctx := r.Context()
	if s.cfg.Budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.Budget)
		defer cancel()
	}

	res, err := s.dl.Download(ctx, raw, spec)
	if err != nil {
		msg := err.Error()
		if msg == "" {
			msg = "下载失败"
		}
		writeErr(w, msg)
		log.Printf("[mj] %s url=%s exit=%s code=500 cost=%.2fs msg=%s",
			r.URL.Path, download.DisplayURL(raw), spec.Redacted(), time.Since(start).Seconds(), msg)
		return
	}

	stored, err := s.storedURL(ctx, r, res)
	if err != nil {
		msg := err.Error()
		writeErr(w, msg)
		log.Printf("[mj] %s url=%s exit=%s code=500 cost=%.2fs msg=%s",
			r.URL.Path, download.DisplayURL(raw), spec.Redacted(), time.Since(start).Seconds(), msg)
		return
	}

	writeOK(w, imageData{
		URL:       stored,
		Key:       res.RelPath,
		Size:      res.FileSize,
		SourceURL: raw,
	})
	log.Printf("[mj] %s url=%s exit=%s code=200 size=%d cost=%.2fs file=%s",
		r.URL.Path, download.DisplayURL(raw), spec.Redacted(), res.FileSize,
		time.Since(start).Seconds(), res.RelPath)
}

// storedURL 决定响应里的 url。
// 配了 CDN_BASE_URL 就用调用方自己的 CDN。
// 配了对象存储且没配 CDN，就用对象在存储上的地址。
// 都没配，就用本服务保存文件的地址。
func (s *Server) storedURL(ctx context.Context, r *http.Request, res download.Result) (string, error) {
	if s.uploader != nil {
		loc, err := s.uploader.Upload(ctx, res.FilePath, res.RelPath)
		if err != nil {
			return "", err
		}
		if err := os.Remove(res.FilePath); err != nil {
			log.Printf("[mj] 对象已上传，删除本地文件失败 %s: %v", res.RelPath, err)
		}
		if s.cfg.CDNBaseURL != "" {
			return joinURL(s.cfg.CDNBaseURL, res.RelPath), nil
		}
		return loc, nil
	}
	if s.cfg.CDNBaseURL != "" {
		return joinURL(s.cfg.CDNBaseURL, res.RelPath), nil
	}
	return s.localURL(r, res.RelPath), nil
}

func (s *Server) localURL(r *http.Request, rel string) string {
	proto := headerFirst(r, "X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := headerFirst(r, "X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return joinURL(proto+"://"+host+"/files", rel)
}

func joinURL(base, rel string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
}

func headerFirst(r *http.Request, key string) string {
	v := r.Header.Get(key)
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

type envelope struct {
	Data any    `json:"data,omitempty"`
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type imageData struct {
	URL       string `json:"url"`
	Key       string `json:"key"`
	Size      int64  `json:"size"`
	SourceURL string `json:"source_url"`
}

func writeOK(w http.ResponseWriter, data imageData) {
	writeJSON(w, http.StatusOK, envelope{Data: data, Code: 200, Msg: "success"})
}

func writeErr(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusOK, envelope{Code: 500, Msg: msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("[mj] panic: %v\n%s", v, debug.Stack())
				writeErr(w, "内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type noListFS struct{ http.FileSystem }

func (n noListFS) Open(name string) (http.File, error) {
	f, err := n.FileSystem.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if stat.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

func fileServer(dir string) http.Handler {
	return http.StripPrefix("/files/", http.FileServer(noListFS{http.Dir(dir)}))
}
