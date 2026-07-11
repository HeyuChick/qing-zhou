# syntax=docker/dockerfile:1
#
# 多阶段构建轻舟面板镜像：① 构建内嵌 Vue 前端 → ② 交叉编译 Go 二进制（含两架构探针）
# → ③ 极小 alpine 运行镜像。纯 Go（modernc sqlite，无 CGO），支持 linux/amd64 与 arm64。
#
# 本地构建：   docker build --build-arg VERSION=v0.2.7 -t qingzhou .
# 多架构构建： docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=v0.2.7 ...

# --- ① 前端（产物内嵌进二进制，必须先于 Go 构建）---
FROM --platform=$BUILDPLATFORM node:20-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npx vite build

# --- ② Go 构建（在 BUILDPLATFORM 上交叉编译到 TARGET，避免 QEMU 慢）---
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
WORKDIR /src
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 用刚构建的前端产物覆盖上下文里的占位 dist（//go:embed all:dist 会嵌入它）
COPY --from=frontend /app/frontend/dist ./frontend/dist
# 面板（目标架构，注入版本号 = 在线更新比较所需）
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X qingzhou/internal/version.Version=${VERSION}" -o /out/qingzhou .
# 探针：两个架构都编（面板按被监控机架构分发 /api/monitor/agent/linux-<arch>）
RUN GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /out/probe/probe-linux-amd64 ./cmd/probe \
 && GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o /out/probe/probe-linux-arm64 ./cmd/probe

# --- ③ 运行镜像 ---
FROM alpine:3.20
# ca-certificates：访问 GitHub(在线更新)/SMTP/SSH 需要；tzdata：正确本地时间；wget(busybox) 供健康检查
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 qingzhou \
 && mkdir -p /data \
 && chown qingzhou:qingzhou /data
COPY --from=builder /out/qingzhou /usr/local/bin/qingzhou
COPY --from=builder /out/probe /opt/qingzhou/probe
ENV QZ_LISTEN=0.0.0.0:8081 \
    QZ_DB=/data/qingzhou.db \
    QZ_USE_NEW_FRONTEND=1 \
    QZ_PROBE_DIR=/opt/qingzhou/probe
EXPOSE 8081
VOLUME ["/data"]
USER qingzhou
WORKDIR /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8081/ >/dev/null 2>&1 || exit 1
ENTRYPOINT ["qingzhou"]
