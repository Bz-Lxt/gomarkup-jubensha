# syntax=docker/dockerfile:1

# ---------------------------------------------------------------- builder
# 镜像必须是 1.25：go.mod 的语言版本被依赖抬到了 go 1.25.0，
# 配合 GOTOOLCHAIN=local 时低于该版本的镜像会在 go mod download 阶段直接失败
# （KB [Go][Docker]）。这里只写主版本，不锁 patch，避免撞上
# 「go.mod 要 1.25.12 而镜像是 1.25.11」那类错配。
FROM golang:1.25-alpine AS builder

# CN 网络三件套之一：apk 换阿里云源。不依赖宿主环境变量传入，
# 保证任何机器上 docker compose build 的行为一致（KB [Docker][CN网络]）。
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache git ca-certificates tzdata

# 三件套之二：GOPROXY 走 goproxy.cn。
ENV GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=sum.golang.google.cn \
    GOTOOLCHAIN=local \
    CGO_ENABLED=0

WORKDIR /src

# 先只拷依赖清单，让 go mod download 的层能被缓存：
# 改业务代码时不必重新拉全部依赖。
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# TARGETOS / TARGETARCH 由 buildx 自动注入，因此同一份 Dockerfile
# 能同时产出 linux/amd64 与 linux/arm64（Apple Silicon 原生）。
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=1.0.0
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/server ./cmd/server

# ------------------------------------------------------------------ final
FROM alpine:3.21

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    # wget 用于 compose healthcheck。alpine 的 busybox 自带 wget，但显式安装
    # 可以避免 busybox 精简变体缺失该 applet 导致 healthcheck 永久 unhealthy
    # （KB [Docker] 记录过 nginx:alpine / mailpit 的同类坑）。
    && apk add --no-cache ca-certificates tzdata wget \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone \
    && addgroup -g 10001 -S app \
    && adduser -u 10001 -S app -G app

ENV TZ=Asia/Shanghai

WORKDIR /app
COPY --from=builder --chown=app:app /out/server /app/server

USER app
EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=12 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/app/server"]
