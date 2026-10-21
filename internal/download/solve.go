package download

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"runtime"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

// cdn.midjourney.com 用的是 Cloudflare Bot Management。
// 多数请求靠浏览器指纹就能拿到图片；真撞上 JS 挑战时，才开一次无头 Chrome
// 把 __cf_bm / cf_clearance 带回来。cookie 同时绑出口 IP 和 UA，
// 所以浏览器必须走和下载完全相同的代理。

var cfCookieNames = []string{"cf_clearance", "__cf_bm", "_cfuvid"}

const challengeWait = 15 * time.Second

type solveOptions struct {
	targetURL string
	proxy     Spec
	timeout   time.Duration
	headless  string
	chromeBin string
}

func solve(ctx context.Context, opt solveOptions) (string, string, error) {
	var last error
	for i := 1; i <= 2; i++ {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		cookie, ua, err := solveOnce(ctx, opt)
		if err == nil {
			log.Printf("[mj] 已取得通行 cookie (%s)", cookieNames(cookie))
			return cookie, ua, nil
		}
		last = err
		if isBlock(err) {
			return "", "", err
		}
		log.Printf("[mj] 第 %d 次过挑战失败: %v", i, err)
	}
	if last == nil {
		last = fmt.Errorf("未能取得通行 cookie")
	}
	return "", "", last
}

func solveOnce(ctx context.Context, opt solveOptions) (cookie, ua string, err error) {
	l := launcher.New().
		Leakless(runtime.GOOS != "windows").
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-blink-features", "AutomationControlled").
		Set("lang", "en-US,en").
		Set("window-size", "1920,1080").
		Delete("enable-automation")

	switch strings.ToLower(strings.TrimSpace(opt.headless)) {
	case "off", "false", "0":
	case "old":
		l = l.Headless(true)
	default:
		l = l.Set("headless", "new")
	}
	if bin := resolveBrowserBin(opt.chromeBin); bin != "" {
		l = l.Bin(bin)
	}

	var bridge *localProxyBridge
	if !opt.proxy.empty() {
		bridge, err = startProxyBridge(opt.proxy, opt.timeout)
		if err != nil {
			return "", "", fmt.Errorf("启动本地代理桥接失败: %v", err)
		}
		defer bridge.Close()
		l = l.Set("proxy-server", "http://"+bridge.Addr())
	}

	controlURL, err := l.Launch()
	if err != nil {
		return "", "", fmt.Errorf("启动浏览器失败: %v；请安装 Chrome / Chromium，或设置 CHROME_BIN", err)
	}
	defer l.Cleanup()

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return "", "", fmt.Errorf("连接浏览器失败: %v", err)
	}
	defer browser.Close()

	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			_ = browser.Close()
		case <-stopped:
		}
	}()

	page, err := stealth.Page(browser)
	if err != nil {
		return "", "", fmt.Errorf("创建页面失败: %v", err)
	}
	timeout := opt.timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	page = page.Timeout(timeout)

	ua, err = browserUserAgent(page)
	if err != nil {
		return "", "", err
	}
	if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent:      ua,
		AcceptLanguage: "en-US,en;q=0.9",
	}); err != nil {
		return "", "", fmt.Errorf("覆盖 UserAgent 失败: %v", err)
	}
	if err := page.Navigate(opt.targetURL); err != nil {
		return "", "", fmt.Errorf("打开目标地址失败: %v", err)
	}

	wait := challengeWait
	if timeout < wait {
		wait = timeout
	}
	cookie, err = waitForChallengePass(browser, page, wait)
	if err != nil {
		return "", "", err
	}
	return cookie, ua, nil
}

func resolveBrowserBin(configured string) string {
	if configured != "" {
		return configured
	}
	if path, ok := launcher.LookPath(); ok {
		return path
	}
	return ""
}

func browserUserAgent(page *rod.Page) (string, error) {
	res, err := page.Eval(`() => navigator.userAgent`)
	if err != nil {
		return "", fmt.Errorf("读取 UserAgent 失败: %v", err)
	}
	return strings.ReplaceAll(res.Value.Str(), "HeadlessChrome/", "Chrome/"), nil
}

func waitForChallengePass(browser *rod.Browser, page *rod.Page, timeout time.Duration) (string, error) {
	start := time.Now()
	deadline := start.Add(timeout)
	lastTitle := ""
	var lastClick time.Time
	clicks := 0

	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		info, err := page.Info()
		if err != nil {
			continue
		}
		lastTitle = info.Title
		if isBlockPage(info.Title) {
			return "", &blockError{msg: fmt.Sprintf("出口 IP 被 Cloudflare 直接封禁 (页面标题=%q)", info.Title)}
		}
		if isChallengeTitle(info.Title) {
			if time.Since(start) > 3*time.Second && clicks < 3 && time.Since(lastClick) > 4*time.Second {
				if clickTurnstile(page) {
					lastClick = time.Now()
					clicks++
					log.Printf("[mj] 挑战页停留 %.1fs，点击 Turnstile (第 %d 次)", time.Since(start).Seconds(), clicks)
				}
			}
			continue
		}
		if cookie := collectCFCookies(browser); cookie != "" {
			return cookie, nil
		}
	}
	return "", fmt.Errorf("等待过挑战超时 (页面标题=%q)", lastTitle)
}

func isChallengeTitle(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	if lower == "" {
		return true
	}
	for _, t := range []string{"just a moment", "verifying you are human", "verify you are human"} {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

const turnstileRectJS = `() => {
	const inp = document.querySelector('input[name="cf-turnstile-response"]');
	if (!inp) return null;
	for (let el = inp.parentElement; el && el !== document.body; el = el.parentElement) {
		const r = el.getBoundingClientRect();
		if (r.width >= 100 && r.height >= 40) {
			return {x: r.left, y: r.top, h: r.height};
		}
	}
	return null;
}`

func clickTurnstile(page *rod.Page) bool {
	res, err := page.Eval(turnstileRectJS)
	if err != nil || res.Value.Nil() {
		return false
	}
	x := res.Value.Get("x").Num() + 30 + rand.Float64()*6
	y := res.Value.Get("y").Num() + res.Value.Get("h").Num()/2 + rand.Float64()*4 - 2
	if err := page.Mouse.MoveLinear(proto.NewPoint(x-120-rand.Float64()*60, y+60+rand.Float64()*40), 1); err != nil {
		return false
	}
	if err := page.Mouse.MoveLinear(proto.NewPoint(x, y), 10+rand.Intn(8)); err != nil {
		return false
	}
	time.Sleep(time.Duration(80+rand.Intn(120)) * time.Millisecond)
	return page.Mouse.Click(proto.InputMouseButtonLeft, 1) == nil
}

func collectCFCookies(browser *rod.Browser) string {
	cookies, err := browser.GetCookies()
	if err != nil {
		return ""
	}
	byName := make(map[string]string, len(cookies))
	for _, c := range cookies {
		if c.Value != "" {
			byName[c.Name] = c.Value
		}
	}
	var parts []string
	for _, name := range cfCookieNames {
		if v, ok := byName[name]; ok {
			parts = append(parts, name+"="+v)
		}
	}
	if len(parts) == 0 || (len(parts) == 1 && strings.HasPrefix(parts[0], "_cfuvid=")) {
		return ""
	}
	return strings.Join(parts, "; ")
}

func cookieNames(cookie string) string {
	var names []string
	for _, part := range strings.Split(cookie, ";") {
		if name, _, ok := strings.Cut(strings.TrimSpace(part), "="); ok {
			names = append(names, name)
		}
	}
	return strings.Join(names, " ")
}
