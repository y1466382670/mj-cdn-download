package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/yanzhiyu/mj-cdn-download/internal/download"
)

type downloadRequest struct {
	URL        string     `json:"url"`
	Proxy      proxyInput `json:"proxy"`
	ProxyAuth  string     `json:"proxyAuth"`
	ProxyAuth2 string     `json:"proxy_auth"`
}

type proxyInput struct {
	spec download.Spec
}

func (p *proxyInput) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var raw string
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
		spec, err := download.Parse(raw, "")
		if err != nil {
			return err
		}
		p.spec = spec
		return nil
	}
	if b[0] != '{' {
		return errors.New("proxy 必须是字符串或对象")
	}

	var obj struct {
		URL      string  `json:"url"`
		Address  string  `json:"address"`
		Host     string  `json:"host"`
		Port     flexInt `json:"port"`
		Type     string  `json:"type"`
		Username string  `json:"username"`
		Password string  `json:"password"`
		Auth     string  `json:"auth"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}

	addr := strings.TrimSpace(obj.Address)
	if addr == "" {
		addr = strings.TrimSpace(obj.URL)
	}
	if addr == "" && strings.TrimSpace(obj.Host) != "" {
		host := strings.TrimSpace(obj.Host)
		if obj.Port > 0 {
			addr = net.JoinHostPort(host, strconv.Itoa(int(obj.Port)))
		} else {
			addr = host
		}
	}
	spec, err := download.Parse(addr, obj.Auth)
	if err != nil {
		return err
	}
	if spec.Type == "" && strings.TrimSpace(obj.Type) != "" {
		spec.Type = strings.ToLower(strings.TrimSpace(obj.Type))
	}
	if obj.Username != "" {
		spec.User = obj.Username
		spec.Pass = obj.Password
	}
	switch spec.Type {
	case "", "http", "https", "socks5", "socks5h":
	default:
		return errors.New("不支持的代理类型，只用 http、https 或 socks5")
	}
	if spec.Addr == "" && (spec.Type != "" || spec.User != "" || spec.Pass != "") {
		return errors.New("代理地址不能为空")
	}
	p.spec = spec
	return nil
}

type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return errors.New("port 必须是数字")
		}
		*f = flexInt(n)
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return errors.New("port 必须是数字")
	}
	*f = flexInt(n)
	return nil
}

func decodeRequest(r io.Reader) (string, download.Spec, error) {
	dec := json.NewDecoder(r)
	var req downloadRequest
	if err := dec.Decode(&req); err != nil {
		return "", download.Spec{}, cleanJSONErr(err)
	}
	auth := strings.TrimSpace(req.ProxyAuth)
	if auth == "" {
		auth = strings.TrimSpace(req.ProxyAuth2)
	}
	spec := req.Proxy.spec
	if auth != "" {
		parsed, err := download.Parse(spec.Addr, auth)
		if err != nil {
			return "", download.Spec{}, err
		}
		spec.User, spec.Pass = parsed.User, parsed.Pass
	}
	raw, err := download.ValidateURL(req.URL)
	if err != nil {
		return "", download.Spec{}, err
	}
	return raw, spec, nil
}

func cleanJSONErr(err error) error {
	if err == nil {
		return nil
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return errors.New("请求体过大")
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errors.New("请求体必须是 JSON")
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return errors.New("请求体必须是 JSON")
	}
	msg := err.Error()
	if strings.Contains(msg, "UnmarshalJSON") {
		if i := strings.LastIndex(msg, ": "); i >= 0 {
			return errors.New(msg[i+2:])
		}
	}
	return errors.New("请求体必须是 JSON")
}
