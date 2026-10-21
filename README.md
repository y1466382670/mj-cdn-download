# mj-cdn-download

Download Midjourney images from `cdn.midjourney.com` and keep them on your machine, your CDN, or your object storage.

**English** | [中文](README.zh-CN.md)

## Introduction

Midjourney puts the finished image on `cdn.midjourney.com`. Plenty of networks cannot open that host, and the original link is not something you want to store in your own site.

So this is a small service you run yourself. Send it the image URL. It downloads the file and gives you a URL on your side. Leave it on the machine, put your own CDN in front, or upload it to Cloudflare R2, Aliyun OSS, or any other S3-compatible store.

`proxy` is optional. Send one and the download goes through it. Leave it out and the service connects directly.

## Deployment

Docker:

```bash
git clone https://github.com/y1466382670/mj-cdn-download.git
cd mj-cdn-download
docker compose up -d --build
```

The image includes Chromium.

Build from source. Chrome must be installed on the machine.

```bash
go build -o mj-cdn-download .
./mj-cdn-download
```

On Windows, use `go build -o mj-cdn-download.exe .`.

The process listens on `:8080`. Open `http://127.0.0.1:8080` for the request example. The page can switch between English and Chinese.

Requests are not authenticated. Deploy it on a private network, and do not publish the port on the public internet.

Other settings are listed in [.env.example](.env.example).

## API

`POST /download`

```json
{
  "url": "https://cdn.midjourney.com/xxxx/0_0.png",
  "proxy": "http://user:pass@host:port"
}
```

`url` is the Midjourney image. Drop `proxy` when you do not need one.

```bash
curl -s http://127.0.0.1:8080/download \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://cdn.midjourney.com/xxxx/0_0.png"}'
```

A saved file looks like this:

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

`url` is the link you use afterwards. `key` is the path inside storage. `size` is the file size in bytes. `source_url` is the address you sent.

When something goes wrong the HTTP status is still 200. Look at `code`. Only `200` means the file was saved.

## Where the file goes

With no extra config, files land in `./data`. `url` looks like `http://127.0.0.1:8080/files/midjourney/20261021/ab12.png`. Nothing deletes old files for you.

You can set a CDN, object storage, or both. With both, the file is uploaded and `url` uses your CDN hostname.

### Your own CDN

The file stays on this machine. Point your domain at `/files/` on this service, then set the prefix that should show up in the response:

```bash
CDN_BASE_URL=https://img.example.com/files
```

`url` becomes `https://img.example.com/files/midjourney/20261021/ab12.png`. No slash at the end. If the domain root already lines up with those files, use `https://img.example.com`.

### Cloudflare R2

Create a bucket, then an API token that can read and write objects. The R2 overview page shows the account ID. The S3 endpoint is `https://<account_id>.r2.cloudflarestorage.com`.

That endpoint is only for the upload. Attach a public hostname to the bucket, either your own domain or the `r2.dev` one, and put it in `CDN_BASE_URL`.

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

Endpoint, bucket, access key, and secret all have to be set. For the R2 hostname you can skip `S3_PATH_STYLE`.

`CDN_BASE_URL` is the bucket root. Do not add `/files`, and do not add a trailing slash. The `url` you get back looks like `https://img.example.com/midjourney/20261021/ab12.png`.

In Docker Compose, change `STORAGE` from `local` to `s3`. `local` ignores every `S3_*` value.

### Aliyun OSS and other S3 stores

Same variables. Swap in that provider's endpoint, region, and bucket. Aliyun's virtual-host addresses want `S3_PATH_STYLE=false`. Add `CDN_BASE_URL` when the link in the response should be your domain.

## License

[MIT](LICENSE)
