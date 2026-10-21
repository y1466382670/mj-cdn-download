package storage

import "testing"

func TestObjectURL(t *testing.T) {
	got, err := ObjectURL("https://oss-cn-hangzhou.aliyuncs.com", "demo", "midjourney/20261021/a.png", false)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://demo.oss-cn-hangzhou.aliyuncs.com/midjourney/20261021/a.png"
	if got != want {
		t.Fatalf("got %s", got)
	}

	got, err = ObjectURL("https://account.r2.cloudflarestorage.com", "images", "midjourney/a.png", true)
	if err != nil {
		t.Fatal(err)
	}
	want = "https://account.r2.cloudflarestorage.com/images/midjourney/a.png"
	if got != want {
		t.Fatalf("got %s", got)
	}
}
