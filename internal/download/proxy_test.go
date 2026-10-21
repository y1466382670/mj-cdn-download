package download

import (
	"strings"
	"testing"
)

func TestParseProxy(t *testing.T) {
	spec, err := Parse("", "")
	if err != nil || !spec.empty() {
		t.Fatalf("direct: %+v %v", spec, err)
	}

	spec, err = Parse("127.0.0.1:1080", "")
	if err != nil || spec.Addr != "127.0.0.1:1080" || spec.Type != "" {
		t.Fatalf("%+v %v", spec, err)
	}

	spec, err = Parse("socks5://user:p@10.0.0.1:1080", "")
	if err != nil || spec.Type != "socks5" || spec.User != "user" || spec.Pass != "p" || spec.Addr != "10.0.0.1:1080" {
		t.Fatalf("%+v %v", spec, err)
	}

	spec, err = Parse("http://10.0.0.1:8080", "name:a:b")
	if err != nil || spec.Type != "http" || spec.User != "name" || spec.Pass != "a:b" {
		t.Fatalf("%+v %v", spec, err)
	}

	if _, err := Parse("", "user:pass"); err == nil {
		t.Fatal("auth without address should fail")
	}
	if _, err := Parse("10.0.0.1", ""); err == nil {
		t.Fatal("missing port should fail")
	}
	if _, err := Parse("ftp://10.0.0.1:21", ""); err == nil {
		t.Fatal("ftp should fail")
	}

	_, err = Parse("https://user:supersecret@10.0.0.1", "")
	if err == nil || strings.Contains(err.Error(), "supersecret") {
		t.Fatal(err)
	}
	_, err = Parse("10.0.0.1:1", "supersecret")
	if err == nil || strings.Contains(err.Error(), "supersecret") {
		t.Fatal(err)
	}
}

func TestRedactedHidesPassword(t *testing.T) {
	spec := Spec{Type: "socks5", Addr: "10.0.0.1:1080", User: "me", Pass: "super-secret"}
	if strings.Contains(spec.Redacted(), "super-secret") || strings.Contains(spec.cacheKey(), "super-secret") {
		t.Fatalf("leaked: %s %s", spec.Redacted(), spec.cacheKey())
	}
	if spec.Redacted() != "socks5 me:***@10.0.0.1:1080" {
		t.Fatal(spec.Redacted())
	}
	if (Spec{}).Redacted() != "直连" {
		t.Fatal((Spec{}).Redacted())
	}
}

func TestTryOrder(t *testing.T) {
	if got := (Spec{Type: "socks5", Addr: "h:1"}).tryOrder(); len(got) != 1 || got[0] != "socks5" {
		t.Fatal(got)
	}
	if got := (Spec{Type: "socks5h", Addr: "h:1"}).tryOrder(); len(got) != 1 || got[0] != "socks5" {
		t.Fatal(got)
	}
	if got := (Spec{Addr: "h:443"}).tryOrder(); got[0] != "https" {
		t.Fatal(got)
	}
	if got := (Spec{Addr: "h:1080"}).tryOrder(); got[0] != "http" {
		t.Fatal(got)
	}
}
