package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/y1466382670/mj-cdn-download/internal/config"
	"github.com/y1466382670/mj-cdn-download/internal/download"
)

type fakeDL struct {
	called bool
	raw    string
	spec   download.Spec
	err    error
	result download.Result
}

func (f *fakeDL) Download(_ context.Context, rawURL string, proxy download.Spec) (download.Result, error) {
	f.called = true
	f.raw = rawURL
	f.spec = proxy
	if f.err != nil {
		return download.Result{}, f.err
	}
	if f.result.RelPath != "" {
		return f.result, nil
	}
	return download.Result{
		FileName: "a.png",
		FilePath: filepath.Join("data", "a.png"),
		RelPath:  "midjourney/20261021/a.png",
		FileSize: 4,
	}, nil
}

type fakeUpload struct {
	local string
	key   string
}

func (f *fakeUpload) Upload(_ context.Context, localPath, key string) (string, error) {
	f.local = localPath
	f.key = key
	return "https://demo.oss-cn-hangzhou.aliyuncs.com/" + key, nil
}

func newTestServer(t *testing.T, dl *fakeDL) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		DataDir:    dir,
		CDNBaseURL: "https://img.example.com/files",
		Budget:     time.Second,
	}
	return httptest.NewServer(New(cfg, dl).Handler())
}

func postJSON(t *testing.T, srv *httptest.Server, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	payload["_raw"] = string(raw)
	payload["_status"] = float64(resp.StatusCode)
	return resp, payload
}

func TestDownloadJSON(t *testing.T) {
	dl := &fakeDL{}
	srv := newTestServer(t, dl)
	defer srv.Close()

	_, payload := postJSON(t, srv, "/download", `{
		"url": "https://cdn.midjourney.com/abc/0_0.png?a=1&b=2",
		"proxy": "socks5://user:secret@10.0.0.1:1080"
	}`)
	if payload["code"] != float64(200) || payload["msg"] != "success" {
		t.Fatalf("%v", payload)
	}
	raw := payload["_raw"].(string)
	if !strings.Contains(raw, "a=1&b=2") || strings.Contains(raw, `\u0026`) {
		t.Fatalf("url escaped: %s", raw)
	}
	if !strings.Contains(raw, `"url":"https://img.example.com/files/midjourney/20261021/a.png"`) {
		t.Fatalf("url: %s", raw)
	}
	if !strings.Contains(raw, `"key":"midjourney/20261021/a.png"`) || !strings.Contains(raw, `"source_url":`) {
		t.Fatalf("data: %s", raw)
	}
	if !dl.called || dl.spec.Type != "socks5" || dl.spec.User != "user" || dl.spec.Pass != "secret" || dl.spec.Addr != "10.0.0.1:1080" {
		t.Fatalf("spec %+v called %v", dl.spec, dl.called)
	}
}

func TestMissingProxyIsDirect(t *testing.T) {
	dl := &fakeDL{}
	srv := newTestServer(t, dl)
	defer srv.Close()

	_, payload := postJSON(t, srv, "/download", `{
		"url": "https://cdn.midjourney.com/abc/0_0.png"
	}`)
	if payload["code"] != float64(200) {
		t.Fatal(payload)
	}
	if dl.spec.Addr != "" || dl.spec.User != "" || dl.spec.Type != "" {
		t.Fatalf("expected direct, got %+v", dl.spec)
	}
}

func TestProxyObjectAndAuth(t *testing.T) {
	dl := &fakeDL{}
	srv := newTestServer(t, dl)
	defer srv.Close()

	_, payload := postJSON(t, srv, "/download", `{
		"url": "https://cdn.midjourney.com/abc/0_0.png",
		"proxy": {"type": "http", "host": "10.0.0.8", "port": "8080", "username": "u", "password": "p"}
	}`)
	if payload["code"] != float64(200) {
		t.Fatal(payload)
	}
	if dl.spec.Type != "http" || dl.spec.Addr != "10.0.0.8:8080" || dl.spec.User != "u" || dl.spec.Pass != "p" {
		t.Fatalf("%+v", dl.spec)
	}

	dl2 := &fakeDL{}
	srv2 := newTestServer(t, dl2)
	defer srv2.Close()
	_, payload = postJSON(t, srv2, "/download", `{
		"url": "https://cdn.midjourney.com/abc/0_0.png",
		"proxy": "10.1.1.1:9000",
		"proxyAuth": "name:secret"
	}`)
	if payload["code"] != float64(200) || dl2.spec.User != "name" || dl2.spec.Pass != "secret" || dl2.spec.Addr != "10.1.1.1:9000" {
		t.Fatalf("%+v %v", dl2.spec, payload)
	}
}

func TestRejectsBadURL(t *testing.T) {
	dl := &fakeDL{}
	srv := newTestServer(t, dl)
	defer srv.Close()

	_, payload := postJSON(t, srv, "/download", `{"url":"https://example.com/a.png"}`)
	if payload["code"] != float64(500) || dl.called {
		t.Fatalf("%v called %v", payload, dl.called)
	}

	_, payload = postJSON(t, srv, "/download", `{}`)
	if payload["msg"] != "url不能为空" {
		t.Fatal(payload)
	}

	_, payload = postJSON(t, srv, "/download", `{"url":"https://cdn.midjourney.com/a.png","proxyAuth":"u:p"}`)
	if payload["code"] != float64(500) || dl.called {
		t.Fatalf("%v", payload)
	}
}

func TestHealthAndFiles(t *testing.T) {
	dl := &fakeDL{}
	srv := newTestServer(t, dl)
	defer srv.Close()

	health, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer health.Body.Close()
	if health.StatusCode != 200 {
		t.Fatal(health.StatusCode)
	}

	index, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer index.Body.Close()
	page, _ := io.ReadAll(index.Body)
	text := string(page)
	if !strings.Contains(text, "cdn.midjourney.com") || strings.Contains(text, "ttapi.io") {
		t.Fatalf("page %s", text)
	}
	if !strings.Contains(text, `data-set-lang="zh"`) || !strings.Contains(text, `data-lang="zh" hidden`) {
		t.Fatalf("page should default to English and offer Chinese: %s", text)
	}
	if strings.Contains(text, "docs.ttapi.io") || strings.Contains(text, "/file/mj-download") {
		t.Fatalf("page exposes internal paths: %s", text)
	}

	file, err := http.Get(srv.URL + "/files/pic.png")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Body.Close()
	got, _ := io.ReadAll(file.Body)
	if file.StatusCode != 200 || string(got) != "png" {
		t.Fatalf("%d %q", file.StatusCode, got)
	}

	dir, err := http.Get(srv.URL + "/files/")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Body.Close()
	if dir.StatusCode == 200 {
		t.Fatal("directory listing should be hidden")
	}
}

func TestDownloadError(t *testing.T) {
	dl := &fakeDL{err: errBoom{}}
	srv := newTestServer(t, dl)
	defer srv.Close()
	resp, payload := postJSON(t, srv, "/download", `{"url":"https://cdn.midjourney.com/a.png"}`)
	if resp.StatusCode != 200 || payload["code"] != float64(500) || payload["msg"] != "boom" {
		t.Fatalf("%d %v", resp.StatusCode, payload)
	}
}

func TestDefaultURLIsLocalFile(t *testing.T) {
	dl := &fakeDL{}
	srv := httptest.NewServer(New(config.Config{DataDir: t.TempDir(), Budget: time.Second}, dl).Handler())
	defer srv.Close()

	_, payload := postJSON(t, srv, "/download", `{"url":"https://cdn.midjourney.com/abc/0_0.png"}`)
	raw := payload["_raw"].(string)
	want := srv.URL + "/files/midjourney/20261021/a.png"
	if payload["code"] != float64(200) || !strings.Contains(raw, want) {
		t.Fatalf("%s", raw)
	}
	if !strings.Contains(raw, `"key":"midjourney/20261021/a.png"`) || strings.Contains(raw, "file_path") || strings.Contains(raw, "new_url") {
		t.Fatalf("data %s", raw)
	}
}

func TestObjectStoragePrefersCallerCDN(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "a.png")
	if err := os.WriteFile(local, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl := &fakeDL{result: download.Result{
		FileName: "a.png",
		FilePath: local,
		RelPath:  "midjourney/20261021/a.png",
		FileSize: 3,
	}}
	up := &fakeUpload{}
	apiSrv := New(config.Config{
		DataDir:    dir,
		CDNBaseURL: "https://img.example.com",
		Budget:     time.Second,
	}, dl)
	apiSrv.UseUploader(up)
	srv := httptest.NewServer(apiSrv.Handler())
	defer srv.Close()

	_, payload := postJSON(t, srv, "/download", `{"url":"https://cdn.midjourney.com/abc/0_0.png"}`)
	raw := payload["_raw"].(string)
	if !strings.Contains(raw, "https://img.example.com/midjourney/20261021/a.png") {
		t.Fatalf("%s", raw)
	}
	if strings.Contains(raw, "oss-cn-hangzhou") {
		t.Fatalf("object url leaked past CDN: %s", raw)
	}
	if up.key != "midjourney/20261021/a.png" {
		t.Fatalf("key %s", up.key)
	}
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Fatal("local file should be removed after upload")
	}
}

func TestHiddenPathIsNotServed(t *testing.T) {
	srv := httptest.NewServer(New(config.Config{DataDir: t.TempDir()}, &fakeDL{}).Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/file/mj-download", "application/json", strings.NewReader(`{"url":"https://cdn.midjourney.com/a.png"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }
