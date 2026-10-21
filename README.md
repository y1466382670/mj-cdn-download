# mj-cdn-download

**English** | [中文](README.zh-CN.md)

Download an image from `cdn.midjourney.com` and keep it on your machine, your CDN, or your object storage.

Midjourney serves generated images from `cdn.midjourney.com`. That host is often unreachable, and the original link is awkward to keep in your own product. Post the image URL here. The service saves the file and returns a URL on your side.

To generate images, use [TTAPI](https://ttapi.io).

## Run

```bash
git clone https://github.com/y1466382670/mj-cdn-download.git
cd mj-cdn-download
docker compose up -d --build
```

The Docker image includes Chromium.

From source:

```bash
go build -o mj-cdn-download .
./mj-cdn-download
```

Windows: `go build -o mj-cdn-download.exe .` Chrome has to be installed.

Listens on `:8080`. Files go to `./data` until you set storage below. Open `http://127.0.0.1:8080` for the request example. Other settings are in [.env.example](.env.example).

## API

`POST /download`

```json
{
  "url": "https://cdn.midjourney.com/xxxx/0_0.png",
  "proxy": "http://user:pass@host:port"
}
```

`proxy` can be omitted. Set it and the download uses that proxy. Omit it and the download connects directly.

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

| Field | Meaning |
| --- | --- |
| `url` | Where to fetch the saved file. Your CDN or bucket URL after you configure storage |
| `key` | Object path, for example `midjourney/20261021/ab12.png` |
| `size` | Size in bytes |
| `source_url` | The Midjourney URL from the request |

A failed download is still HTTP 200. Read `code`. `200` means the file was saved.

## Storage

With nothing else set, `url` is `http://<host>/files/` plus `key`. Old files stay in `data` until you delete them.

A CDN and object storage can be set alone or together. Together, the file is uploaded and `url` uses your CDN host.

### Your CDN

The file stays on this machine. Point the domain at `/files/` on this service.

```bash
CDN_BASE_URL=https://img.example.com/files
```

`url` is `https://img.example.com/files/midjourney/20261021/ab12.png`. No trailing slash. If the domain root already matches the object path, use `https://img.example.com`.

### Cloudflare R2

Create a bucket and an API token that can read and write objects. The account ID is on the R2 overview. The S3 endpoint is `https://<account_id>.r2.cloudflarestorage.com`.

Use that endpoint for upload. Bind a public hostname on the bucket (your domain or `r2.dev`) and put it in `CDN_BASE_URL`.

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

Endpoint, bucket, access key, and secret are all required. `S3_PATH_STYLE` can be left unset for `r2.cloudflarestorage.com`.

`CDN_BASE_URL` is the bucket root. No trailing slash, and no `/files`. `url` comes back as `https://img.example.com/midjourney/20261021/ab12.png`.

In Docker Compose, set `STORAGE` to `s3`. `STORAGE=local` ignores `S3_*`.

### Other S3 storage

Aliyun OSS and other S3-compatible stores use the same `S3_*` variables. Change the endpoint, region, and bucket. Virtual-host URLs need `S3_PATH_STYLE=false`. Set `CDN_BASE_URL` when the response should use your domain.

## License

[MIT](LICENSE)
