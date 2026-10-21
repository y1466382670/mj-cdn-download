# mj-cdn-download

把 Midjourney 的图片从 `cdn.midjourney.com` 下载下来，存到你自己的机器、CDN 或对象存储。

[English](README.md) | **中文**

## 简介

Midjourney 生成的图片放在 `cdn.midjourney.com`。这个域名很多网络打不开，原链接也不适合直接挂在自己的网站上。

所以做了这个服务，自己部署就行。把图片地址传过来，它把文件存下来，再给你一个你这边能打开的地址。可以只放在本机，也可以套上你自己的 CDN，或者上传到 Cloudflare R2、阿里云 OSS，以及其他 S3 兼容存储。

`proxy` 可传可不传。传了就走这个代理，不传就直连。

## 部署

使用 Docker：

```bash
git clone https://github.com/y1466382670/mj-cdn-download.git
cd mj-cdn-download
docker compose up -d --build
```

镜像已包含 Chromium。

从源码构建时，本机需要安装 Chrome：

```bash
go build -o mj-cdn-download .
./mj-cdn-download
```

Windows 使用 `go build -o mj-cdn-download.exe .`。

服务监听 `:8080`。访问 `http://127.0.0.1:8080` 可查看请求示例，页面支持中英文切换。

接口不校验身份。请部署在内网，不要把端口暴露到公网。

其余配置见 [.env.example](.env.example)。

## 接口

`POST /download`

```json
{
  "url": "https://cdn.midjourney.com/xxxx/0_0.png",
  "proxy": "http://user:pass@host:port"
}
```

`url` 是 Midjourney 的图片地址。不需要代理时，把 `proxy` 去掉。

```bash
curl -s http://127.0.0.1:8080/download \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://cdn.midjourney.com/xxxx/0_0.png"}'
```

存好之后会返回：

```json
{
  "code": 200,
  "msg": "success",
  "data": {
    "url": "http://127.0.0.1:8080/files/midjourney/20261021/ab12.png",
    "key": "midjourney/20261021/ab12.png",
    "size": 123456,
    "source_url": "https://cdn.midjourney.com/xxxx/0_0.png"
  }
}
```

`url` 是之后要用的地址，`key` 是文件在存储里的路径，`size` 是字节数，`source_url` 是你传进来的原地址。

失败的时候 HTTP 状态码也是 200，看返回里的 `code`。只有 `200` 才是成功。

## 文件存哪儿

什么都不配时，文件写到 `./data`，`url` 类似 `http://127.0.0.1:8080/files/midjourney/20261021/ab12.png`。旧文件不会自动删。

CDN 和对象存储可以单独配，也可以一起配。一起配的话，文件会上传，返回的地址用你的 CDN 域名。

### 自己的 CDN

文件还在这台机器上。把你的域名回源到这个服务的 `/files/`，再告诉服务返回地址用哪个前缀：

```bash
CDN_BASE_URL=https://img.example.com/files
```

这时 `url` 是 `https://img.example.com/files/midjourney/20261021/ab12.png`。结尾不要加斜杠。域名根目录已经对着这些文件的话，写成 `https://img.example.com`。

### Cloudflare R2

先建桶，再创建一个能读写对象的 API 令牌。R2 概览页上有 Account ID，S3 地址是 `https://<account_id>.r2.cloudflarestorage.com`。

这个地址只负责上传。给桶绑一个能公开访问的域名，自己的域名或者 `r2.dev` 都行，填到 `CDN_BASE_URL`。

```bash
STORAGE=s3
S3_ENDPOINT=https://<account_id>.r2.cloudflarestorage.com
S3_REGION=auto
S3_BUCKET=your-bucket
S3_ACCESS_KEY=your-access-key
S3_SECRET_KEY=your-secret-key
S3_PATH_STYLE=true
CDN_BASE_URL=https://img.example.com
```

Endpoint、桶名、Access Key、Secret 四个都要填。R2 的域名可以不写 `S3_PATH_STYLE`。

`CDN_BASE_URL` 填桶的根域名，不要加 `/files`，结尾也不要斜杠。返回的 `url` 类似 `https://img.example.com/midjourney/20261021/ab12.png`。

Docker Compose 里把 `STORAGE` 从 `local` 改成 `s3`。还写着 `local` 的话，`S3_*` 不会生效。

### 阿里云 OSS 和其他 S3

同一组变量，换成对应的 Endpoint、Region 和桶名。阿里云这种虚拟主机风格的地址，把 `S3_PATH_STYLE` 设为 `false`。希望返回的链接走自己的域名，再配上 `CDN_BASE_URL`。
