package download

import (
	"errors"
	"net/url"
	"path"
	"strings"
)

const Host = "cdn.midjourney.com"

// ValidateURL 只接受 https://cdn.midjourney.com 上的图片地址。
func ValidateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("url不能为空")
	}
	if len(raw) > 4096 {
		return "", errors.New("url过长")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("url无法解析")
	}
	if !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), Host) {
		return "", errors.New("只支持 https://" + Host + " 的图片地址")
	}
	if u.User != nil {
		return "", errors.New("图片地址不能带账号")
	}
	if p := u.Port(); p != "" && p != "443" {
		return "", errors.New("只支持 https://" + Host + " 的图片地址")
	}
	if u.Path == "" || u.Path == "/" {
		return "", errors.New("图片地址缺少路径")
	}
	return raw, nil
}

// DisplayURL 去掉 query，避免日志带上签名参数。
func DisplayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "<invalid URL>"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func extFromContentType(contentType string) string {
	mime := strings.TrimSpace(strings.ToLower(strings.Split(contentType, ";")[0]))
	switch mime {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	case "image/bmp":
		return ".bmp"
	default:
		return ""
	}
}

func extFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := u.Path
	if decoded, derr := url.PathUnescape(p); derr == nil {
		p = decoded
	}
	return cleanExt(strings.ToLower(path.Ext(p)))
}

func cleanExt(ext string) string {
	if ext == "" || len(ext) > 8 || ext[0] != '.' {
		return ""
	}
	for _, c := range ext[1:] {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return ""
		}
	}
	return ext
}

func detectExt(b []byte) string {
	n := len(b)
	switch {
	case n >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return ".jpg"
	case n >= 4 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G':
		return ".png"
	case n >= 3 && b[0] == 'G' && b[1] == 'I' && b[2] == 'F':
		return ".gif"
	case n >= 12 && b[0] == 'R' && b[1] == 'I' && b[2] == 'F' && b[3] == 'F' &&
		b[8] == 'W' && b[9] == 'E' && b[10] == 'B' && b[11] == 'P':
		return ".webp"
	case n >= 2 && b[0] == 'B' && b[1] == 'M':
		return ".bmp"
	case n >= 12 && string(b[4:8]) == "ftyp" && string(b[8:12]) == "avif":
		return ".avif"
	}
	sample := b
	if len(sample) > 64 {
		sample = sample[:64]
	}
	lower := strings.ToLower(string(sample))
	if strings.Contains(lower, "<!do") || strings.Contains(lower, "<htm") {
		return ".html"
	}
	return ""
}

func isHTML(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/html")
}
