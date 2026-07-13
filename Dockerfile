# 多阶段构建：最终镜像仅含运行时二进制（无 Python）
FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/pool-proxy ./cmd/pool-proxy \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/poolctl ./cmd/poolctl \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/render-config ./cmd/render-config

FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --home-dir /data --shell /usr/sbin/nologin pool \
 && mkdir -p /data /etc/pool-proxy \
 && chown -R pool:pool /data

COPY --from=builder /out/pool-proxy /usr/local/bin/pool-proxy
COPY --from=builder /out/poolctl /usr/local/bin/poolctl
COPY --from=builder /out/render-config /usr/local/bin/render-config
COPY config.example.yaml /etc/pool-proxy/config.example.yaml
COPY deploy/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh /usr/local/bin/render-config

ENV POOL_DATA_DIR=/data \
    POOL_CONFIG=/data/config.yaml

WORKDIR /data
USER pool
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=5 \
  CMD curl -fsS http://127.0.0.1:8080/readyz || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["pool-proxy", "--config", "/data/config.yaml"]
