package download

import (
	"io"
	"strings"
	"testing"

	http "github.com/Danny-Dasilva/fhttp"
)

func TestValidateURL(t *testing.T) {
	ok := "https://cdn.midjourney.com/abc/0_0.png?width=100"
	got, err := ValidateURL("  " + ok + "  ")
	if err != nil || got != ok {
		t.Fatalf("%q %v", got, err)
	}
	cases := []string{
		"",
		"http://cdn.midjourney.com/a.png",
		"https://cdn.midjourney.com.evil.com/a.png",
		"https://evil.com/cdn.midjourney.com/a.png",
		"https://user:pass@cdn.midjourney.com/a.png",
		"https://cdn.midjourney.com:8443/a.png",
		"https://cdn.midjourney.com",
		"https://cdn.midjourney.com/",
		"https://CDN.MIDJOURNEY.COM/a.png",
	}
	wantOK := map[string]bool{
		"https://CDN.MIDJOURNEY.COM/a.png": true,
	}
	for _, raw := range cases {
		_, err := ValidateURL(raw)
		if wantOK[raw] {
			if err != nil {
				t.Fatalf("%s: %v", raw, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := ValidateURL(""); err == nil || !strings.Contains(err.Error(), "url不能为空") {
		t.Fatal(err)
	}
}

func TestDisplayURLDropsQuery(t *testing.T) {
	got := DisplayURL("https://cdn.midjourney.com/a/0_0.png?sig=secret")
	if strings.Contains(got, "secret") || got != "https://cdn.midjourney.com/a/0_0.png" {
		t.Fatal(got)
	}
}

func TestDetectExt(t *testing.T) {
	if detectExt([]byte{0x89, 'P', 'N', 'G', 0, 0, 0, 0}) != ".png" {
		t.Fatal("png")
	}
	if detectExt([]byte("<!doctype html>")) != ".html" {
		t.Fatal("html")
	}
	if cleanExt(".png/../") != "" {
		t.Fatal("bad ext")
	}
}

func TestDocumentHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://cdn.midjourney.com/a.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	p := browserProfiles[0]
	setDocumentHeaders(req, requestOptions{profile: p, ua: p.ua, secCHUA: p.secCHUA})
	if _, ok := req.Header["referer"]; ok {
		t.Fatal("document request must not send referer")
	}
	if got := req.Header["sec-fetch-dest"]; len(got) != 1 || got[0] != "document" {
		t.Fatalf("dest %v", got)
	}
	if got := req.Header["sec-fetch-mode"]; len(got) != 1 || got[0] != "navigate" {
		t.Fatalf("mode %v", got)
	}
	if got := req.Header["sec-fetch-site"]; len(got) != 1 || got[0] != "none" {
		t.Fatalf("site %v", got)
	}
}

func TestDescribeFailure(t *testing.T) {
	resp := &http.Response{
		StatusCode: 403,
		Header: http.Header{
			"Content-Type": []string{"text/html"},
		},
		Body: io.NopCloser(strings.NewReader("<html><title>Just a moment...</title></html>")),
	}
	if err := describeFailure(resp, nil); !isChallenge(err) {
		t.Fatal(err)
	}

	resp = &http.Response{
		StatusCode: 403,
		Header:     http.Header{"Cf-Mitigated": []string{"challenge"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	if err := describeFailure(resp, nil); !isChallenge(err) {
		t.Fatal(err)
	}

	resp = &http.Response{
		StatusCode: 403,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader("<html><title>Sorry, you have been blocked</title></html>")),
	}
	if err := describeFailure(resp, nil); !isBlock(err) {
		t.Fatal(err)
	}

	resp = &http.Response{
		StatusCode: 404,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"missing"}`)),
	}
	err := describeFailure(resp, nil)
	if !isPermanent(err) || isChallenge(err) {
		t.Fatal(err)
	}
}
