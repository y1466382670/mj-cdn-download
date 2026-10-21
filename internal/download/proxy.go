package download

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

const maxDialTimeout = 12 * time.Second

// Spec 是这一次请求要用的代理。Addr 为空表示直连。
type Spec struct {
	Type string
	Addr string
	User string
	Pass string
}

func (s Spec) empty() bool {
	return strings.TrimSpace(s.Addr) == ""
}

// Redacted 给日志用，不带密码。
func (s Spec) Redacted() string {
	if s.empty() {
		return "直连"
	}
	typ := s.Type
	if typ == "" {
		typ = "代理"
	}
	if s.User == "" {
		return typ + " " + s.Addr
	}
	return typ + " " + s.User + ":***@" + s.Addr
}

func (s Spec) cacheKey() string {
	if s.empty() {
		return "direct"
	}
	sum := sha256.Sum256([]byte(s.User + "\x00" + s.Pass))
	return strings.ToLower(s.Type) + "|" + s.Addr + "|" + hex.EncodeToString(sum[:8])
}

// Parse 解析代理地址。addr 可以是 host:port，或带协议和账号的 URL。
// auth 形如 user:pass，会覆盖 URL 里的账号。两者都空就是直连。
func Parse(addr, auth string) (Spec, error) {
	addr = strings.TrimSpace(addr)
	auth = strings.TrimSpace(auth)
	if addr == "" {
		if auth != "" {
			return Spec{}, errors.New("传了代理账号但没有代理地址")
		}
		return Spec{}, nil
	}

	spec := Spec{Addr: addr}
	if strings.Contains(addr, "://") {
		u, err := url.Parse(addr)
		if err != nil || u.Host == "" {
			return Spec{}, errors.New("代理地址无法解析")
		}
		spec.Type = strings.ToLower(u.Scheme)
		spec.Addr = u.Host
		if u.User != nil {
			spec.User = u.User.Username()
			spec.Pass, _ = u.User.Password()
		}
	}
	if auth != "" {
		user, pass, ok := strings.Cut(auth, ":")
		if !ok || user == "" {
			return Spec{}, errors.New("代理账号格式应为 user:pass")
		}
		spec.User, spec.Pass = user, pass
	}
	switch spec.Type {
	case "", "http", "https", "socks5", "socks5h":
	default:
		return Spec{}, errors.New("不支持的代理类型，只用 http、https 或 socks5")
	}
	if _, _, err := net.SplitHostPort(spec.Addr); err != nil {
		return Spec{}, errors.New("代理地址格式应为 host:port")
	}
	return spec, nil
}

// dial 连到 target（host:port）。没写协议时自动探测，并记住这个地址可用的那种。
func (s Spec) dial(target string, timeout time.Duration) (net.Conn, error) {
	if timeout > maxDialTimeout {
		timeout = maxDialTimeout
	}
	if _, cached := cachedProxyType(s.Addr); !cached && s.Type == "" {
		lock := probeLock(s.Addr)
		lock.Lock()
		if _, cached := cachedProxyType(s.Addr); !cached {
			conn, err := s.dialResolved(target, timeout)
			lock.Unlock()
			return conn, err
		}
		lock.Unlock()
	}
	return s.dialResolved(target, timeout)
}

func (s Spec) dialResolved(target string, timeout time.Duration) (net.Conn, error) {
	order := s.tryOrder()
	if cached, ok := cachedProxyType(s.Addr); ok && s.Type == "" {
		order = moveToFront(order, cached)
	}
	var errs []string
	for _, typ := range order {
		conn, err := s.dialAs(typ, target, timeout)
		if err == nil {
			rememberProxyType(s.Addr, typ)
			return conn, nil
		}
		if isProxyPolicy(err) {
			return nil, err
		}
		errs = append(errs, typ+": "+err.Error())
	}
	return nil, fmt.Errorf("代理连接失败: %s", strings.Join(errs, " | "))
}

func (s Spec) tryOrder() []string {
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case "socks5", "socks5h":
		return []string{"socks5"}
	case "http":
		return []string{"http"}
	case "https":
		return []string{"https"}
	default:
		if strings.HasSuffix(s.Addr, ":443") {
			return []string{"https", "socks5", "http"}
		}
		return []string{"http", "socks5", "https"}
	}
}

func (s Spec) dialAs(typ, target string, timeout time.Duration) (net.Conn, error) {
	switch typ {
	case "socks5", "socks5h":
		return s.dialSOCKS5(target, timeout)
	case "http":
		return s.dialHTTP(target, timeout, false)
	case "https":
		return s.dialHTTP(target, timeout, true)
	default:
		return nil, fmt.Errorf("不支持的代理类型: %s", typ)
	}
}

func (s Spec) dialSOCKS5(target string, timeout time.Duration) (net.Conn, error) {
	var auth *proxy.Auth
	if s.User != "" {
		auth = &proxy.Auth{User: s.User, Password: s.Pass}
	}
	dialer, err := proxy.SOCKS5("tcp", s.Addr, auth, &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 SOCKS5 代理失败: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cd, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("SOCKS5 代理不支持带超时的拨号")
	}
	conn, err := cd.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, fmt.Errorf("通过 SOCKS5 代理连接失败: %v", err)
	}
	return conn, nil
}

func (s Spec) dialHTTP(target string, timeout time.Duration, useTLS bool) (net.Conn, error) {
	raw, err := net.DialTimeout("tcp", s.Addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("连接代理失败: %v", err)
	}
	_ = raw.SetDeadline(time.Now().Add(timeout))

	var conn net.Conn = raw
	if useTLS {
		host, _, splitErr := net.SplitHostPort(s.Addr)
		if splitErr != nil {
			host = s.Addr
		}
		// 只跳过代理这一跳的证书校验。很多代理的 443 端口证书和域名对不上，
		// 目标站点 cdn.midjourney.com 仍然走正常的证书校验。
		tlsConn := tls.Client(raw, &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		})
		if err := tlsConn.Handshake(); err != nil {
			raw.Close()
			return nil, fmt.Errorf("代理 TLS 握手失败: %v", err)
		}
		conn = tlsConn
	}

	connect := "CONNECT " + target + " HTTP/1.1\r\n" +
		"Host: " + target + "\r\n" +
		"User-Agent: " + browserProfiles[0].ua + "\r\n" +
		"Proxy-Connection: Keep-Alive\r\n"
	if s.User != "" {
		token := base64.StdEncoding.EncodeToString([]byte(s.User + ":" + s.Pass))
		connect += "Proxy-Authorization: Basic " + token + "\r\n"
	}
	connect += "\r\n"
	if _, err := io.WriteString(conn, connect); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送 CONNECT 失败: %v", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取代理响应失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		conn.Close()
		msg := strings.Join(strings.Fields(string(detail)), " ")
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "mainland china") || strings.Contains(lower, "banned") {
			return nil, fmt.Errorf("代理拒绝当前出口 IP: %s", truncate(msg, 180))
		}
		if msg == "" {
			return nil, fmt.Errorf("代理 CONNECT 失败: %s", resp.Status)
		}
		return nil, fmt.Errorf("代理 CONNECT 失败: %s (%s)", resp.Status, truncate(msg, 160))
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("重置代理连接超时失败: %v", err)
	}
	if n := br.Buffered(); n > 0 {
		leftover := make([]byte, n)
		if _, err := io.ReadFull(br, leftover); err == nil {
			return &prefixConn{Conn: conn, prefix: leftover}, nil
		}
	}
	return conn, nil
}

type prefixConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

func isProxyPolicy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "banned") || strings.Contains(msg, "mainland china") ||
		strings.Contains(msg, "代理拒绝当前出口")
}

var (
	proxyTypeMu    sync.RWMutex
	proxyTypeCache = map[string]string{}
	probeLocks     sync.Map
)

func probeLock(addr string) *sync.Mutex {
	v, _ := probeLocks.LoadOrStore(addr, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func cachedProxyType(addr string) (string, bool) {
	proxyTypeMu.RLock()
	defer proxyTypeMu.RUnlock()
	t, ok := proxyTypeCache[addr]
	return t, ok
}

func rememberProxyType(addr, typ string) {
	proxyTypeMu.Lock()
	defer proxyTypeMu.Unlock()
	proxyTypeCache[addr] = typ
}

func moveToFront(order []string, want string) []string {
	out := []string{want}
	for _, t := range order {
		if t != want {
			out = append(out, t)
		}
	}
	return out
}

// localProxyBridge 在本机开一个不需要认证的 HTTP 代理，把流量转给上游。
// Chrome 不支持带账号的 SOCKS5，过挑战时只能这样接。
type localProxyBridge struct {
	ln      net.Listener
	spec    Spec
	timeout time.Duration
	once    sync.Once
}

func startProxyBridge(spec Spec, timeout time.Duration) (*localProxyBridge, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	if timeout > maxDialTimeout {
		timeout = maxDialTimeout
	}
	b := &localProxyBridge{ln: ln, spec: spec, timeout: timeout}
	go b.serve()
	return b, nil
}

func (b *localProxyBridge) Addr() string { return b.ln.Addr().String() }

func (b *localProxyBridge) Close() {
	b.once.Do(func() { _ = b.ln.Close() })
}

func (b *localProxyBridge) serve() {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			return
		}
		go b.handle(conn)
	}
}

func (b *localProxyBridge) handle(client net.Conn) {
	defer client.Close()
	br := bufio.NewReader(client)
	r, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if r.Method != http.MethodConnect {
		_, _ = io.WriteString(client, "HTTP/1.1 405 Method Not Allowed\r\n\r\n")
		return
	}
	upstream, err := b.spec.dial(r.Host, b.timeout)
	if err != nil {
		_, _ = io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer upstream.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	if n := br.Buffered(); n > 0 {
		pending := make([]byte, n)
		if _, err := io.ReadFull(br, pending); err == nil {
			if _, err := upstream.Write(pending); err != nil {
				return
			}
		}
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
}
