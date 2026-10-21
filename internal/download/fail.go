package download

import (
	"errors"
	"strconv"
	"strings"
	"time"

	http "github.com/Danny-Dasilva/fhttp"
)

type challengeError struct{ msg string }

func (e *challengeError) Error() string { return e.msg }

func isChallenge(err error) bool {
	var c *challengeError
	return errors.As(err, &c)
}

type blockError struct{ msg string }

func (e *blockError) Error() string { return e.msg }

func isBlock(err error) bool {
	var b *blockError
	return errors.As(err, &b)
}

type httpStatusError struct {
	msg        string
	retryAfter time.Duration
	permanent  bool
}

func (e *httpStatusError) Error() string { return e.msg }

func isPermanent(err error) bool {
	var h *httpStatusError
	return errors.As(err, &h) && h.permanent
}

func retryAfterOf(err error) time.Duration {
	var h *httpStatusError
	if errors.As(err, &h) {
		return h.retryAfter
	}
	return 0
}

func describeFailure(resp *http.Response, tr *trace) error {
	body, err := readBody(resp, 64<<10)
	if err != nil {
		tr.logf("读取错误页失败: %v", err)
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	if title := pageTitle(text); title != "" {
		tr.logf("%d 响应页标题: %q", resp.StatusCode, title)
	} else if text != "" {
		tr.logf("%d 响应内容: %s", resp.StatusCode, truncate(text, 200))
	}

	switch mitigated := strings.ToLower(strings.TrimSpace(resp.Header.Get("cf-mitigated"))); mitigated {
	case "challenge", "managed_challenge", "jschallenge", "captcha", "interactive":
		return &challengeError{msg: sprintfChallenge(resp, "Cloudflare 要求过挑战", mitigated)}
	case "block", "drop":
		return &blockError{msg: sprintfChallenge(resp, "出口 IP 被 Cloudflare 封禁", mitigated)}
	}

	if isBlockPage(text) && looksCloudflare(resp, text) {
		return &blockError{msg: sprintfChallenge(resp, "出口 IP 被 Cloudflare 封禁", "")}
	}
	if isCloudflareChallenge(text) {
		return &challengeError{msg: sprintfChallenge(resp, "Cloudflare 挑战页", "")}
	}

	msg := sprintfChallenge(resp, "HTTP", "")
	if text != "" {
		msg += ": " + truncate(text, 200)
	}
	if edgeMitigation(resp, text) {
		return &challengeError{msg: "疑似 Cloudflare 边缘拦截: " + msg}
	}
	return &httpStatusError{
		msg:        msg,
		retryAfter: parseRetryAfter(resp),
		permanent:  permanentStatus(resp.StatusCode),
	}
}

func sprintfChallenge(resp *http.Response, what, mitigated string) string {
	ray := resp.Header.Get("CF-Ray")
	if ray == "" {
		ray = "-"
	}
	if mitigated != "" {
		return what + " (状态=" + strconv.Itoa(resp.StatusCode) + ", cf-mitigated=" + mitigated + ", CF-Ray: " + ray + ")"
	}
	if what == "HTTP" {
		return "HTTP " + strconv.Itoa(resp.StatusCode) + " (CF-Ray: " + ray + ")"
	}
	return what + " (状态=" + strconv.Itoa(resp.StatusCode) + ", CF-Ray: " + ray + ")"
}

func permanentStatus(code int) bool {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusGone, http.StatusUnavailableForLegalReasons:
		return true
	default:
		return false
	}
}

func parseRetryAfter(resp *http.Response) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func edgeMitigation(resp *http.Response, text string) bool {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	if resp.Header.Get("CF-Ray") == "" {
		return false
	}
	return text == "" || looksCloudflare(resp, text)
}

func looksCloudflare(resp *http.Response, text string) bool {
	if isHTML(resp.Header.Get("Content-Type")) {
		return true
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "cloudflare") || strings.Contains(lower, "error code:")
}

func isCloudflareChallenge(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"just a moment",
		"cf-browser-verification",
		"cf_chl_opt",
		"challenge-platform",
		"enable javascript and cookies to continue",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isBlockPage(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"attention required",
		"you have been blocked",
		"error code: 1006",
		"error code: 1007",
		"error code: 1008",
		"error code: 1020",
		"access denied",
		"banned",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func pageTitle(text string) string {
	lower := strings.ToLower(text)
	open := strings.Index(lower, "<title")
	if open < 0 {
		return ""
	}
	gt := strings.IndexByte(text[open:], '>')
	if gt < 0 {
		return ""
	}
	rest := text[open+gt+1:]
	end := strings.Index(strings.ToLower(rest), "</title>")
	if end < 0 {
		return ""
	}
	return truncate(rest[:end], 120)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
