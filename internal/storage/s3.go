package storage

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Options 描述一个 S3 兼容的对象存储。阿里云 OSS、Cloudflare R2、MinIO 都走这套参数。
type Options struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	PathStyle bool
}

// S3 把下载好的图片上传到对象存储。
type S3 struct {
	client    *s3.Client
	bucket    string
	endpoint  string
	pathStyle bool
}

func NewS3(opt Options) (*S3, error) {
	if opt.Region == "" {
		opt.Region = "auto"
	}
	client := s3.New(s3.Options{
		Region:       opt.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(opt.AccessKey, opt.SecretKey, ""),
		BaseEndpoint: aws.String(opt.Endpoint),
		UsePathStyle: opt.PathStyle,
	})
	return &S3{
		client:    client,
		bucket:    opt.Bucket,
		endpoint:  strings.TrimRight(opt.Endpoint, "/"),
		pathStyle: opt.PathStyle,
	}, nil
}

// Upload 上传本地文件，返回对象自身的访问地址。调用方如果另外配了 CDN，会改用 CDN 地址。
func (s *S3) Upload(ctx context.Context, localPath, key string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("打开本地文件失败: %w", err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("读取本地文件失败: %w", err)
	}

	key = cleanKey(key)
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(key)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(stat.Size()),
		ContentType:   aws.String(contentType),
		CacheControl:  aws.String("public, max-age=31536000"),
	})
	if err != nil {
		return "", fmt.Errorf("上传对象存储失败: %v", err)
	}
	return ObjectURL(s.endpoint, s.bucket, key, s.pathStyle)
}

// ObjectURL 拼出对象在存储上的地址。pathStyle 为真时是 endpoint/bucket/key，否则是 bucket.endpoint-host/key。
func ObjectURL(endpoint, bucket, key string, pathStyle bool) (string, error) {
	key = cleanKey(key)
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("对象存储 Endpoint 无法解析")
	}
	u.RawQuery = ""
	u.Fragment = ""
	if pathStyle {
		base := strings.TrimRight(u.Path, "/")
		u.Path = base + "/" + bucket + "/" + key
		return u.String(), nil
	}
	host := bucket + "." + u.Hostname()
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	u.Host = host
	u.Path = "/" + key
	return u.String(), nil
}

func cleanKey(key string) string {
	key = strings.ReplaceAll(key, "\\", "/")
	return strings.TrimLeft(key, "/")
}
