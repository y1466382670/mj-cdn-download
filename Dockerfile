FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/mj-cdn-download .

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates chromium \
    && rm -rf /var/lib/apt/lists/*

ENV PORT=8080 \
    DATA_DIR=/data \
    CHROME_BIN=/usr/bin/chromium \
    HEADLESS=new \
    SOLVE=true

WORKDIR /app
COPY --from=build /out/mj-cdn-download /app/mj-cdn-download

VOLUME /data
EXPOSE 8080

ENTRYPOINT ["/app/mj-cdn-download"]
