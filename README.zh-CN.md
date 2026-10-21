# mj-cdn-download

[English](README.md) | **中文**

把 `cdn.midjourney.com` 上的图片下载下来，存到自己的机器、CDN 或对象存储。

Midjourney 的图放在 `cdn.midjourney.com`。这个地址经常访问不到，也不适合直接留在自己的业务里。把图片地址 POST 给这个服务，它存好文件，再返回你这边的地址。

要出图的话，用 [TTAPI](https://ttapi.io)。

## 运行

```bash
git clone https://github.com/y1466382670/mj-cdn-download.git
cd mj-cdn-download
docker compose up -d --build
```

Docker 镜像里带了 Chromium。

本机：

```bash
go build -o mj-cdn-download .
./mj-cdn-download
```

Windows 用 `go build -o mj-cdn-download.exe .`，并且要装 Chrome。

默认端口 `8080`。没配下面的存储时，文件在 `./data`。打开 `http://127.0.0.1:8080` 能看到请求示例。其他参数在 [.env.example](.env.example)。

## 接口

`POST /download`

```json
{
  "url": "https://cdn.midjourney.com/xxxx/0_0.png",
  "proxy": "http://user:pass@host:port"
}
```

`proxy` 可以不传。传了就走这个代理，不传就直连。

```bash
curl -s http://127.0.0.1:8080/download \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://cdn.midjourney.com/xxxx/0_0.png","proxy":"http://user:pass@host:port"}'
```

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

| 字段 | 含义 |
| --- | --- |
| `url` | 保存后的图片地址。配了 CDN 或对象存储之后，这里就是你的地址 |
| `key` | 对象路径，例如 `midjourney/20261021/ab12.png` |
| `size` | 字节数 |
| `source_url` | 请求里的 Midjourney 地址 |

失败时 HTTP 也是 200，看 `code`。`200` 才是存好了。

## 存储

什么都不配时，`url` 是 `http://<主机>/files/` 加上 `key`。`data` 里的旧文件不会自动删。

CDN 和对象存储可以分开配，也可以一起配。一起配时，文件上传到桶里，`url` 用你的 CDN 域名。

### 自己的 CDN

文件还在这台机器上。把域名回源到本服务的 `/files/`。

```bash
CDN_BASE_URL=https://img.example.com/files
```

`url` 会变成 `https://img.example.com/files/midjourney/20261021/ab12.png`。末尾不要斜杠。域名根目录已经对应对象路径时，写成 `https://img.example.com`。

### Cloudflare R2

建一个桶，再创建一个能读写对象的 API 令牌。Account ID 在 R2 概览页，S3 地址是 `https://<account_id>.r2.cloudflarestorage.com`。

这个地址用来上传。桶上再绑一个公开域名（自己的域名或 `r2.dev`），填到 `CDN_BASE_URL`。

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

Endpoint、桶名、Access Key、Secret 都要填。Endpoint 是 `r2.cloudflarestorage.com` 时，`S3_PATH_STYLE` 可以不写。

`CDN_BASE_URL` 是桶的根域名，末尾不要斜杠，也不要加 `/files`。返回的 `url` 类似 `https://img.example.com/midjourney/20261021/ab12.png`。

Docker Compose 里把 `STORAGE` 改成 `s3`。`STORAGE=local` 时，填了 `S3_*` 也不会上传。

### 其他 S3 存储

阿里云 OSS 和其他 S3 兼容存储用同一组 `S3_*`，换成自己的 Endpoint、Region 和桶名。虚拟主机风格的地址把 `S3_PATH_STYLE` 设为 `false`。想让返回地址走自己的域名，再配 `CDN_BASE_URL`。

## 许可

[MIT](LICENSE)
