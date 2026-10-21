package download

import (
	"compress/flate"
	"compress/gzip"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	http "github.com/Danny-Dasilva/fhttp"
	"github.com/Danny-Dasilva/fhttp/http2"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	utls "github.com/refraction-networking/utls"
)

// Cloudflare 同时看 TLS 指纹、HTTP/2 SETTINGS 和请求头顺序。
// 标准库的 net/http 即便套一层 uTLS，HTTP/2 伪头顺序和 SETTINGS 也不是 Chrome，
// 这种组合本身就会被拦住。所以这里用 fhttp 把这三层对齐到同一份 Chrome 画像。

type browserProfile struct {
	name       string
	hello      utls.ClientHelloID
	ua         string
	secCHUA    string
	forceHTTP1 bool
}

var browserProfiles = []browserProfile{
	{
		name:    "chrome133",
		hello:   utls.HelloChrome_133,
		ua:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
		secCHUA: `"Not(A:Brand";v="99", "Google Chrome";v="133", "Chromium";v="133"`,
	},
	{
		name:    "chrome131",
		hello:   utls.HelloChrome_131,
		ua:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		secCHUA: `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
	},
	{
		name:    "chrome120-mac",
		hello:   utls.HelloChrome_120,
		ua:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		secCHUA: `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`,
	},
	{
		name:       "chrome133-http1",
		hello:      utls.HelloChrome_133,
		ua:         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
		secCHUA:    `"Not(A:Brand";v="99", "Google Chrome";v="133", "Chromium";v="133"`,
		forceHTTP1: true,
	},
}

func profileForAttempt(attempt int) browserProfile {
	if attempt < 1 {
		attempt = 1
	}
	return browserProfiles[(attempt-1)%len(browserProfiles)]
}

// profileFor 在有通行 cookie 时挑和签发 UA 最接近的画像。
// cookie 按签发时的 UA 校验，TLS 版本如果差太远，组合本身就不存在。
func profileFor(attempt int, clearanceUA string) browserProfile {
	want := chromeMajor(clearanceUA)
	if want == 0 {
		return profileForAttempt(attempt)
	}
	best := -1
	var candidates []browserProfile
	for _, p := range browserProfiles {
		gap := want - chromeMajor(p.ua)
		if gap < 0 {
			gap = -gap
		}
		switch {
		case best < 0 || gap < best:
			best, candidates = gap, []browserProfile{p}
		case gap == best:
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return profileForAttempt(attempt)
	}
	if attempt < 1 {
		attempt = 1
	}
	return candidates[(attempt-1)%len(candidates)]
}

func chromeMajor(ua string) int {
	idx := strings.Index(ua, "Chrome/")
	if idx < 0 {
		return 0
	}
	rest := ua[idx+len("Chrome/"):]
	if dot := strings.IndexByte(rest, '.'); dot >= 0 {
		rest = rest[:dot]
	}
	major, err := strconv.Atoi(rest)
	if err != nil {
		return 0
	}
	return major
}

func handshake(raw net.Conn, host string, profile browserProfile, timeout time.Duration) (*utls.UConn, error) {
	spec, err := utls.UTLSIdToSpec(profile.hello)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("加载 TLS 指纹失败: %v", err)
	}
	if profile.forceHTTP1 {
		for _, ext := range spec.Extensions {
			if alpn, ok := ext.(*utls.ALPNExtension); ok {
				alpn.AlpnProtocols = []string{"http/1.1"}
			}
		}
	}

	conn := utls.UClient(raw, &utls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
	}, utls.HelloCustom)
	if err := conn.ApplyPreset(&spec); err != nil {
		raw.Close()
		return nil, fmt.Errorf("应用 TLS 指纹失败: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := conn.Handshake(); err != nil {
		raw.Close()
		return nil, fmt.Errorf("TLS 握手失败: %v", err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func newTransport(negotiated string, dial func(network, addr string) (net.Conn, error)) http.RoundTripper {
	if negotiated == "http/1.1" {
		return &http.Transport{
			DialTLS:            dial,
			DisableCompression: true,
			IdleConnTimeout:    90 * time.Second,
		}
	}
	return &http2.Transport{
		DialTLS: func(network, addr string, _ *utls.Config) (net.Conn, error) {
			return dial(network, addr)
		},
		Navigator: http2.Chrome,
		// fhttp 自带的 Chrome 预设多了 SETTINGS 3 和 5、少了 2，和真实 Chrome 对不上。
		// HTTP2Settings 的优先级高于 ApplyPreset，所以从这里改。
		HTTP2Settings: &http2.HTTP2Settings{
			Settings: []http2.Setting{
				{ID: http2.SettingHeaderTableSize, Val: 65536},
				{ID: http2.SettingEnablePush, Val: 0},
				{ID: http2.SettingInitialWindowSize, Val: 6291456},
				{ID: http2.SettingMaxHeaderListSize, Val: 262144},
			},
			ConnectionFlow: 15663105,
		},
		DisableCompression: true,
		ReadIdleTimeout:    30 * time.Second,
		PingTimeout:        10 * time.Second,
	}
}

const acceptEncoding = "gzip, deflate, br, zstd"

type requestOptions struct {
	profile browserProfile
	ua      string
	secCHUA string
	cookie  string
}

// setDocumentHeaders 按「在地址栏直接打开图片」写头：
// document 导航、不带 referer。cdn.midjourney.com 对这种请求直接回图片。
func setDocumentHeaders(req *http.Request, opt requestOptions) {
	type pair struct{ key, value string }
	pairs := []pair{}
	if opt.secCHUA != "" {
		pairs = append(pairs,
			pair{"sec-ch-ua", opt.secCHUA},
			pair{"sec-ch-ua-mobile", "?0"},
			pair{"sec-ch-ua-platform", platformFor(opt.ua)},
		)
	}
	pairs = append(pairs,
		pair{"upgrade-insecure-requests", "1"},
		pair{"user-agent", opt.ua},
		pair{"accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"},
		pair{"sec-fetch-site", "none"},
		pair{"sec-fetch-mode", "navigate"},
		pair{"sec-fetch-user", "?1"},
		pair{"sec-fetch-dest", "document"},
		pair{"accept-encoding", acceptEncoding},
		pair{"accept-language", "en-US,en;q=0.9"},
		pair{"priority", "u=0, i"},
	)
	if opt.cookie != "" {
		pairs = append(pairs, pair{"cookie", opt.cookie})
	}

	header := http.Header{}
	order := make([]string, 0, len(pairs)+1)
	for _, p := range pairs {
		header[p.key] = []string{p.value}
		order = append(order, p.key)
	}
	if opt.cookie == "" {
		order = append(order, "cookie")
	}
	header[http.HeaderOrderKey] = order
	header[http.PHeaderOrderKey] = []string{":method", ":authority", ":scheme", ":path"}
	req.Header = header
}

func platformFor(ua string) string {
	switch {
	case strings.Contains(ua, "Windows"):
		return `"Windows"`
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		return `"macOS"`
	case strings.Contains(ua, "Android"):
		return `"Android"`
	case strings.Contains(ua, "Linux"), strings.Contains(ua, "X11"):
		return `"Linux"`
	default:
		return `"Windows"`
	}
}

func secCHUAFor(userAgent string) string {
	major := chromeMajor(userAgent)
	if major == 0 {
		return ""
	}
	return fmt.Sprintf(`"Not(A:Brand";v="99", "Google Chrome";v="%d", "Chromium";v="%d"`, major, major)
}

func decompressBody(resp *http.Response) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))) {
	case "", "identity":
		return resp.Body, nil
	case "gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("解压 gzip 失败: %v", err)
		}
		return zr, nil
	case "deflate":
		return flate.NewReader(resp.Body), nil
	case "br":
		return brotli.NewReader(resp.Body), nil
	case "zstd":
		zr, err := zstd.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("解压 zstd 失败: %v", err)
		}
		return zr.IOReadCloser(), nil
	default:
		return resp.Body, nil
	}
}

func readBody(resp *http.Response, limit int64) ([]byte, error) {
	r, err := decompressBody(resp)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}
	return body, nil
}
