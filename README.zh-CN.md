# mj-cdn-download

[English](README.md) | **中文**

自建的 Midjourney 图片下载与转存服务。提交 `cdn.midjourney.com` 上的图片地址，服务将文件保存到你自己的存储，并返回可访问的地址。

## 背景

Midjourney 生成的图片托管在 `cdn.midjourney.com`。业务系统要长期保存这些结果、换成自己的域名，或交给后续流程使用时，需要先把文件从源站取回，再写入自己掌控的位置。

直接访问源站并不稳定。部分网络会被拦截，源站地址也不适合作为业务侧的永久链接。本服务把这一步收成一个可自行部署的 HTTP 接口：调用方提交图片地址，服务完成下载，并把文件放到本机、你的 CDN，或你的对象存储上。

它面向需要自己掌握图片落盘和分发的团队。若要直接调用 Midjourney 出图，可以使用 [TTAPI](https://ttapi.io)。

## 部署

```bash
git clone https://github.com/y1466382670/mj-cdn-download.git
cd mj-cdn-download
docker compose up -d --build
```

本机构建：

```bash
go build -o mj-cdn-download .
./mj-cdn-download
```

默认监听 `:8080`。未配置外部存储时，文件写入 `./data`，由本服务提供访问。浏览器打开 `http://127.0.0.1:8080` 可查看调用示例，页面默认是英文，可切换为中文。

其余运行参数见 [.env.example](.env.example)。

## 接口

`POST /download`，请求体为 JSON。

```json
{
  "url": "https://cdn.midjourney.com/xxxx/0_0.png",
  "proxy": "http://user:pass@host:port"
}
```

`url` 为 Midjourney 图片地址。`proxy` 可选：传入则通过该代理访问源站，不传则由服务所在网络直接访问。

```bash
curl -s http://127.0.0.1:8080/download \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://cdn.midjourney.com/xxxx/0_0.png","proxy":"http://user:pass@host:port"}'
```

成功时 `code` 为 `200`：

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
| `url` | 保存后的图片地址。配置 CDN 或对象存储后，这里是你自己的地址 |
| `key` | 存储键，与对象在桶中的路径一致 |
| `size` | 文件大小，单位为字节 |
| `source_url` | 请求中的原图地址 |

失败时 HTTP 状态仍为 200，以响应体中的 `code` 判断。`code` 为 `200` 才表示成功。

## 存储

默认文件保存在本服务，`url` 为 `http://<主机>/files/` 与 `key` 拼接的结果。

自有 CDN 与对象存储可以单独配置，也可以同时配置。同时配置时，文件上传到对象存储，`url` 使用你的 CDN 域名。

### 自有 CDN

文件仍保存在本服务的 `DATA_DIR`。将你的域名回源到本服务的 `/files/`，再设置返回地址使用的前缀：

```bash
CDN_BASE_URL=https://img.example.com/files
```

此时 `url` 为 `https://img.example.com/files/midjourney/20261021/ab12.png`。前缀不要带末尾斜杠。若域名根目录直接对应存储键，则写成 `https://img.example.com`。

### Cloudflare R2

在 Cloudflare R2 中创建存储桶，并创建具备对象读写权限的 API 令牌。R2 概览页提供 Account ID，对应的 S3 地址为 `https://<Account ID>.r2.cloudflarestorage.com`。

该 S3 地址用于上传，不能作为图片的公开链接。请在存储桶上绑定公开域名（自定义域名或 `r2.dev`），并把该域名写入 `CDN_BASE_URL`。

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

`S3_ENDPOINT`、`S3_BUCKET`、`S3_ACCESS_KEY`、`S3_SECRET_KEY` 四项都需要填写。Endpoint 包含 `r2.cloudflarestorage.com` 时，`S3_PATH_STYLE` 可以省略，服务会按 `true` 处理。

`CDN_BASE_URL` 指向存储桶的根目录，不要带末尾斜杠，也不要附加 `/files`。成功后 `url` 为 `https://img.example.com/midjourney/20261021/ab12.png`。

使用 Docker Compose 时，将 `STORAGE` 从 `local` 改为 `s3`，并填入上述变量。`STORAGE=local` 时，已填写的 `S3_*` 不会生效。

### 其他对象存储

阿里云 OSS 以及其他提供 S3 接口的存储，使用同一组 `S3_*`。将 Endpoint、Region 和桶名换成对应服务商的值。地址为虚拟主机风格时，设置 `S3_PATH_STYLE=false`。需要对外域名时，同样配置 `CDN_BASE_URL`。

## 许可

[MIT](LICENSE)
