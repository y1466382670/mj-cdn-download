package download

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	http "github.com/Danny-Dasilva/fhttp"
)

const (
	maxBodySize      = 256 << 20
	maxDownloadTime  = 10 * time.Minute
	clearanceTTL     = 20 * time.Minute
	clientIdle       = 5 * time.Minute
	maxCachedClients = 32
	minSolveBudget   = 13 * time.Second
)

// Options 控制下载器。代理不在这里，由每次请求传入。
type Options struct {
	DataDir     string
	Timeout     time.Duration
	MaxRetry    int
	MaxInflight int
	Headless    string
	ChromeBin   string
	Solve       bool
}

// Result 是一次成功下载的落盘结果。
type Result struct {
	FileName string
	FilePath string
	RelPath  string
	FileSize int64
	Exit     string
}

type clearance struct {
	cookie string
	ua     string
	at     time.Time
}

type solveCall struct {
	done       chan struct{}
	cookie, ua string
	err        error
}

type cachedClient struct {
	client *http.Client
	last   time.Time
}

// Service 下载 cdn.midjourney.com 上的图片。
type Service struct {
	dataDir   string
	timeout   time.Duration
	maxRetry  int
	headless  string
	chromeBin string
	solveOn   bool

	sem      chan struct{}
	solveSem chan struct{}

	mu       sync.Mutex
	cookies  map[string]*clearance
	inflight map[string]*solveCall

	clientsMu sync.Mutex
	clients   map[string]*cachedClient

	stop     chan struct{}
	stopOnce sync.Once
}

func New(opt Options) *Service {
	if opt.Timeout <= 0 {
		opt.Timeout = 30 * time.Second
	}
	if opt.MaxRetry <= 0 {
		opt.MaxRetry = 3
	}
	if opt.MaxInflight <= 0 {
		opt.MaxInflight = 32
	}
	if opt.Headless == "" {
		opt.Headless = "new"
	}
	s := &Service{
		dataDir:   opt.DataDir,
		timeout:   opt.Timeout,
		maxRetry:  opt.MaxRetry,
		headless:  opt.Headless,
		chromeBin: opt.ChromeBin,
		solveOn:   opt.Solve,
		sem:       make(chan struct{}, opt.MaxInflight),
		solveSem:  make(chan struct{}, 2),
		cookies:   map[string]*clearance{},
		inflight:  map[string]*solveCall{},
		clients:   map[string]*cachedClient{},
		stop:      make(chan struct{}),
	}
	go s.sweep()
	return s
}

func (s *Service) Close() {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.clientsMu.Lock()
		defer s.clientsMu.Unlock()
		for key, c := range s.clients {
			c.client.CloseIdleConnections()
			delete(s.clients, key)
		}
	})
}

// Download 把 rawURL 下载到数据目录。proxy 为空则直连，不为空则只用这个代理。
func (s *Service) Download(ctx context.Context, rawURL string, proxy Spec) (Result, error) {
	rawURL, err := ValidateURL(rawURL)
	if err != nil {
		return Result{}, err
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return Result{}, userContextErr(ctx.Err())
	}

	day := time.Now().Format("20060102")
	relDir := path.Join("midjourney", day)
	saveDir := filepath.Join(s.dataDir, filepath.FromSlash(relDir))
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("创建目录失败: %v", err)
	}

	key := Host + "|" + proxy.cacheKey()
	tr := newTrace()
	tr.logf("出口 %s", proxy.Redacted())

	var last error
	solves := 0
	for attempt := 1; attempt <= s.maxRetry; attempt++ {
		if err := ctx.Err(); err != nil {
			last = userContextErr(err)
			break
		}
		cookie, ua := s.cookieGet(key)
		tr.logf("第 %d/%d 次，cookie=%t", attempt, s.maxRetry, cookie != "")
		res, err := s.downloadOnce(ctx, rawURL, saveDir, relDir, proxy, cookie, ua, attempt, tr)
		if err == nil {
			res.Exit = proxy.Redacted()
			return res, nil
		}
		last = sanitizeErr(rawURL, err)
		tr.logf("第 %d 次失败: %v", attempt, last)
		switch {
		case isBlock(err):
			if proxy.empty() {
				last = fmt.Errorf("%s；直连被封，请在请求里传入 proxy", last.Error())
			} else {
				last = fmt.Errorf("%s；请换一个代理", last.Error())
			}
			goto done
		case isPermanent(err) || isProxyPolicy(err) || isContextErr(err):
			goto done
		}
		if isChallenge(err) {
			s.cookieInvalidate(key, cookie)
			if !s.solveOn {
				last = fmt.Errorf("%s；未开启浏览器过挑战（SOLVE=false）", last.Error())
				break
			}
			if solves >= 2 || !budgetOK(ctx) {
				if !budgetOK(ctx) {
					last = fmt.Errorf("%s；剩余时间不够过挑战", last.Error())
				}
				break
			}
			solves++
			if _, _, serr := s.solveShared(ctx, key, rawURL, proxy); serr != nil {
				if isBlock(serr) {
					last = fmt.Errorf("%s；请换一个代理", sanitizeErr(rawURL, serr).Error())
				} else {
					last = fmt.Errorf("%s；自动过挑战失败: %v", last.Error(), sanitizeErr(rawURL, serr))
				}
				break
			}
			continue
		}
		if attempt == s.maxRetry {
			break
		}
		wait := time.Duration(attempt) * time.Second
		if ra := retryAfterOf(err); ra > wait {
			wait = ra
		}
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= wait {
			last = fmt.Errorf("%s；需要等待 %s 后才值得重试，已超出本次预算", last.Error(), wait.Round(time.Second))
			break
		}
		if err := sleepCtx(ctx, wait); err != nil {
			last = userContextErr(err)
			break
		}
	}
done:
	tr.dump(DisplayURL(rawURL))
	if last == nil {
		last = errors.New("下载失败")
	}
	return Result{}, last
}

func (s *Service) downloadOnce(callerCtx context.Context, rawURL, saveDir, relDir string, proxy Spec, cookie, cookieUA string, attempt int, tr *trace) (Result, error) {
	profile := profileFor(attempt, cookieUA)
	client := s.clientFor(profile, proxy)

	ctx, cancel := context.WithTimeout(callerCtx, maxDownloadTime)
	defer cancel()

	headBudget := s.timeout
	if dl, ok := callerCtx.Deadline(); ok {
		if left := time.Until(dl); left < headBudget {
			headBudget = left
		}
	}
	if headBudget < time.Second {
		headBudget = time.Second
	}
	headGuard := time.AfterFunc(headBudget, cancel)
	defer headGuard.Stop()

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, fmt.Errorf("创建请求失败: %v", err)
	}
	req = req.WithContext(ctx)

	ua := profile.ua
	sec := profile.secCHUA
	if cookieUA != "" {
		ua = cookieUA
		if got := secCHUAFor(cookieUA); got != "" {
			sec = got
		}
	}
	setDocumentHeaders(req, requestOptions{
		profile: profile,
		ua:      ua,
		secCHUA: sec,
		cookie:  cookie,
	})

	guard := startStallGuard(cancel, s.timeout)
	defer guard.stopGuard()

	resp, err := client.Do(req)
	headGuard.Stop()
	if err != nil {
		return Result{}, fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	tr.logf("响应 %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode != http.StatusOK {
		return Result{}, describeFailure(resp, tr)
	}

	body, err := decompressBody(resp)
	if err != nil {
		return Result{}, err
	}
	if c, ok := body.(io.Closer); ok && c != resp.Body {
		defer c.Close()
	}
	body = guard.watch(io.LimitReader(body, maxBodySize+1))

	head := make([]byte, 512)
	n, err := io.ReadFull(body, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Result{}, fmt.Errorf("读取响应失败: %v", err)
	}
	head = head[:n]

	detected := detectExt(head)
	contentType := resp.Header.Get("Content-Type")
	if isHTML(contentType) || detected == ".html" {
		return Result{}, &challengeError{msg: fmt.Sprintf("返回了 HTML 而不是图片 (CF-Ray: %s)", rayOf(resp))}
	}

	ext := cleanExt(detected)
	if ext == "" {
		ext = cleanExt(extFromContentType(contentType))
	}
	if ext == "" {
		ext = extFromURL(rawURL)
	}
	if ext == "" {
		ext = ".bin"
	}
	name, err := randomName(ext)
	if err != nil {
		return Result{}, err
	}
	full := filepath.Join(saveDir, name)
	written, err := writeStream(full, head, body)
	if err != nil {
		return Result{}, err
	}
	if written == 0 {
		return Result{}, errors.New("响应内容为空")
	}
	if written > maxBodySize {
		_ = os.Remove(full)
		return Result{}, errors.New("文件超过 256MB")
	}
	return Result{
		FileName: name,
		FilePath: full,
		RelPath:  path.Join(relDir, name),
		FileSize: written,
	}, nil
}

func (s *Service) clientFor(profile browserProfile, proxy Spec) *http.Client {
	key := profile.name + "|" + proxy.cacheKey()
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if c, ok := s.clients[key]; ok {
		c.last = time.Now()
		return c.client
	}

	want := "h2"
	if profile.forceHTTP1 {
		want = "http/1.1"
	}
	dialTimeout := s.timeout
	if dialTimeout > maxDialTimeout {
		dialTimeout = maxDialTimeout
	}
	dial := func(network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		var raw net.Conn
		if proxy.empty() {
			raw, err = net.DialTimeout("tcp", addr, dialTimeout)
			if err != nil {
				return nil, fmt.Errorf("TCP 连接失败: %v", err)
			}
		} else {
			raw, err = proxy.dial(addr, dialTimeout)
			if err != nil {
				return nil, err
			}
		}
		conn, err := handshake(raw, host, profile, dialTimeout)
		if err != nil {
			return nil, err
		}
		if got := conn.ConnectionState().NegotiatedProtocol; got != want {
			conn.Close()
			return nil, fmt.Errorf("ALPN 协商出 %q，与期望的 %q 不符", got, want)
		}
		return conn, nil
	}

	c := &http.Client{
		Transport: newTransport(want, dial),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("太多重定向")
			}
			if !strings.EqualFold(req.URL.Hostname(), Host) {
				return errors.New("重定向离开了 cdn.midjourney.com")
			}
			for key, val := range via[0].Header {
				req.Header[key] = val
			}
			return nil
		},
	}
	if len(s.clients) >= maxCachedClients {
		s.evictClientLocked()
	}
	s.clients[key] = &cachedClient{client: c, last: time.Now()}
	return c
}

func (s *Service) evictClientLocked() {
	var victim string
	var oldest time.Time
	for key, c := range s.clients {
		if victim == "" || c.last.Before(oldest) {
			victim, oldest = key, c.last
		}
	}
	if victim != "" {
		s.clients[victim].client.CloseIdleConnections()
		delete(s.clients, victim)
	}
}

func (s *Service) sweep() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.sweepClients()
			s.sweepCookies()
		}
	}
}

func (s *Service) sweepClients() {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for key, c := range s.clients {
		if time.Since(c.last) > clientIdle {
			c.client.CloseIdleConnections()
			delete(s.clients, key)
		}
	}
}

func (s *Service) sweepCookies() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, c := range s.cookies {
		if time.Since(c.at) > clearanceTTL {
			delete(s.cookies, key)
		}
	}
}

func (s *Service) freshLocked(key string) (*clearance, bool) {
	c, ok := s.cookies[key]
	if !ok || c.cookie == "" || time.Since(c.at) > clearanceTTL {
		return nil, false
	}
	return c, true
}

func (s *Service) cookieGet(key string) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.freshLocked(key)
	if !ok {
		return "", ""
	}
	return c.cookie, c.ua
}

func (s *Service) cookiePut(key, cookie, ua string) {
	if cookie == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookies[key] = &clearance{cookie: cookie, ua: ua, at: time.Now()}
}

func (s *Service) cookieInvalidate(key, used string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cookies[key]
	if !ok {
		return
	}
	if used == "" && c.cookie != "" {
		return
	}
	if used == "" || c.cookie == used {
		delete(s.cookies, key)
	}
}

func (s *Service) solveShared(ctx context.Context, key, rawURL string, proxy Spec) (string, string, error) {
	s.mu.Lock()
	if c, ok := s.freshLocked(key); ok {
		s.mu.Unlock()
		return c.cookie, c.ua, nil
	}
	if call, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", "", userContextErr(ctx.Err())
		case <-call.done:
			return call.cookie, call.ua, call.err
		}
	}
	call := &solveCall{done: make(chan struct{})}
	s.inflight[key] = call
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.inflight, key)
		s.mu.Unlock()
		close(call.done)
	}()

	select {
	case s.solveSem <- struct{}{}:
		defer func() { <-s.solveSem }()
	case <-ctx.Done():
		call.err = userContextErr(ctx.Err())
		return "", "", call.err
	}

	log.Printf("[mj] 开始过 Cloudflare 挑战，出口 %s", proxy.Redacted())
	cookie, ua, err := solve(ctx, solveOptions{
		targetURL: rawURL,
		proxy:     proxy,
		timeout:   solveTimeout(ctx),
		headless:  s.headless,
		chromeBin: s.chromeBin,
	})
	call.cookie, call.ua, call.err = cookie, ua, err
	if err == nil {
		s.cookiePut(key, cookie, ua)
	}
	return cookie, ua, err
}

func solveTimeout(ctx context.Context) time.Duration {
	const maxWait = 25 * time.Second
	dl, ok := ctx.Deadline()
	if !ok {
		return maxWait
	}
	left := time.Until(dl) - 5*time.Second
	if left < 8*time.Second {
		return 8 * time.Second
	}
	if left > maxWait {
		return maxWait
	}
	return left
}

func budgetOK(ctx context.Context) bool {
	dl, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(dl) >= minSolveBudget
}

func writeStream(full string, head []byte, rest io.Reader) (int64, error) {
	f, err := os.Create(full)
	if err != nil {
		return 0, fmt.Errorf("创建文件失败: %v", err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 64<<10)
	n, err := w.Write(head)
	if err != nil {
		_ = os.Remove(full)
		return 0, fmt.Errorf("保存文件失败: %v", err)
	}
	copied, err := io.Copy(w, rest)
	if err != nil {
		_ = os.Remove(full)
		return 0, fmt.Errorf("保存文件失败: %v", err)
	}
	if err := w.Flush(); err != nil {
		_ = os.Remove(full)
		return 0, fmt.Errorf("保存文件失败: %v", err)
	}
	return int64(n) + copied, nil
}

func randomName(ext string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成文件名失败: %v", err)
	}
	return hex.EncodeToString(b[:]) + ext, nil
}

func rayOf(resp *http.Response) string {
	if ray := resp.Header.Get("CF-Ray"); ray != "" {
		return ray
	}
	return "-"
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func userContextErr(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("下载超时")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("调用方已断开")
	}
	return err
}

func sanitizeErr(rawURL string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if rawURL != "" {
		msg = strings.ReplaceAll(msg, rawURL, DisplayURL(rawURL))
	}
	if u := queryOf(rawURL); u != "" {
		msg = strings.ReplaceAll(msg, u, "...")
	}
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}

func queryOf(raw string) string {
	u, err := parseQuery(raw)
	if err != nil {
		return ""
	}
	return u
}

func parseQuery(raw string) (string, error) {
	i := strings.IndexByte(raw, '?')
	if i < 0 || i+1 >= len(raw) {
		return "", errors.New("no query")
	}
	q := raw[i+1:]
	if h := strings.IndexByte(q, '#'); h >= 0 {
		q = q[:h]
	}
	if q == "" {
		return "", errors.New("empty")
	}
	return q, nil
}
