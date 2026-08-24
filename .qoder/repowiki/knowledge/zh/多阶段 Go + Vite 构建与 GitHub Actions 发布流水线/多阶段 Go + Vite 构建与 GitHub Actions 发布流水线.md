---
kind: build_system
name: 多阶段 Go + Vite 构建与 GitHub Actions 发布流水线
category: build_system
scope:
    - '**'
source_files:
    - Dockerfile
    - .github/workflows/ci.yml
    - .github/workflows/docker.yml
    - .github/workflows/release.yml
    - internal/version/version.go
    - frontend/embed.go
    - cmd/probe/build.sh
    - install.sh
    - deploy/qingzhou.service
    - go.mod
---

## 1. 构建系统概览

本项目采用 **Go 单二进制 + Vue 3 SPA 内嵌** 的单体交付模式，通过 Docker 多阶段构建、GitHub Actions CI/CD 和 shell 安装脚本完成从源码到生产镜像/二进制的全流程。

- **语言与工具链**：Go 1.25（`go.mod`）、Node 20（Vite 构建前端）、Alpine Linux 运行环境。所有依赖通过 `go mod` 管理，前端通过 `frontend/package.json` 管理。
- **构建产物**：一个面板二进制 `qingzhou`（内嵌前端静态资源）、两个架构探针 `probe-linux-{amd64,arm64}`、以及 release 时额外编译的带 `with_v2ray_api` 标签的 `sing-box` 二进制。
- **版本注入**：通过 `-ldflags "-X qingzhou/internal/version.Version=<tag>"` 在编译期注入版本号；开发构建默认 `Version = "dev"`，被在线更新逻辑视为“未知版本，始终提示最新”。

## 2. 关键文件与职责

| 文件 | 作用 |
|---|---|
| `Dockerfile` | 三阶段构建：① Node 20 Alpine 构建前端 → ② Go 1.25 Alpine 交叉编译面板+双架构探针 → ③ alpine:3.20 最小运行镜像（非 root、健康检查） |
| `.github/workflows/ci.yml` | PR/推送触发：`go build ./...`、`go vet`、`go test -race`、前端 `npx vite build`、shell 语法检查 |
| `.github/workflows/docker.yml` | Release 事件或手动 dispatch 构建并推送 GHCR 多架构镜像（linux/amd64 + linux/arm64），打 `<tag>` 与 `latest` |
| `.github/workflows/release.yml` | 发布流水线：构建面板+探针+自定义 sing-box，使用 `tools/sign` 对二进制签名，上传 SHA256SUMS 至 GitHub Release |
| `internal/version/version.go` | 提供 `Version`、`IsDev()`、`Compare(a,b)` 语义化版本比较，供在线更新模块判断是否可升级 |
| `frontend/embed.go` | 用 `//go:embed all:dist` 将 Vite 产物嵌入二进制；开发时可通过 `QZ_WEB_DIR=frontend/dist` 环境变量回退到磁盘目录 |
| `cmd/probe/build.sh` | 独立于主仓库的探针本地交叉编译脚本（仅 amd64/arm64） |
| `install.sh` | 一键安装/升级脚本：检测 systemd、下载对应架构二进制、校验 SHA256、原子替换、写入 service 单元 |
| `deploy/qingzhou.service` | systemd 单元文件，配合 `qingzhou.env` 环境变量模板 |

## 3. 架构与约定

### 3.1 前端内嵌策略
- 前端位于 `frontend/`，使用 Vite 构建输出到 `frontend/dist/`。
- Go 代码通过 `//go:embed all:dist` 将 `frontend/dist` 打包进二进制；CI 中为节省 npm install 开销，会先放置占位 `index.html` 让 `go build` 能通过。
- 运行时若设置 `QZ_WEB_DIR` 环境变量，则跳过 embed，直接读取磁盘目录，便于开发调试。

### 3.2 交叉编译与多架构
- 面板二进制：`GOOS=linux GOARCH={amd64,arm64} go build -trimpath -ldflags "-s -w -X qingzhou/internal/version.Version=${VERSION}"`
- 探针二进制：同样以 `GOOS=linux GOARCH=amd64/arm64` 分别编译，产物命名为 `probe-linux-amd64` / `probe-linux-arm64`。
- sing-box：release 流水线从源码编译，启用 `with_gvisor,with_quic,with_grpc,...,with_v2ray_api` 等 tag，并通过 `checklinkname=0` 绕过上游对 crypto/tls 内部符号的链接限制。
- Docker 构建使用 `docker buildx` + QEMU 同时产出 amd64 与 arm64 镜像。

### 3.3 版本与在线更新
- 二进制内置版本由 release tag 注入，`internal/updater` 通过 GitHub API 获取最新发布版本并与 `version.Compare` 结果决定是否提示更新。
- 安装脚本 `install.sh` 通过 `releases/latest` 重定向或 GitHub API 解析最新版本号，支持 `--version vX.Y.Z` 指定目标版本。

### 3.4 安全签名
- release 流水线调用 `go run ./tools/sign` 对面板二进制生成 `.sig` 文件，签名密钥来自 `RELEASE_SIGNING_KEY`，公钥通过 `-X qingzhou/internal/updater.ReleasePublicKey=` 注入二进制。
- 若未配置公钥，流水线发出 warning 但继续构建（兼容 fork 场景）；若只配置了公钥而未配置私钥则直接失败，防止产出不签名的二进制。

## 4. 约定与约束

- **CGO 禁用**：Dockerfile 显式设置 `CGO_ENABLED=0`，确保二进制纯 Go，可在任意 Linux 架构上运行（modernc sqlite）。
- **最小运行镜像**：最终镜像仅包含 ca-certificates、tzdata、wget（busybox），以非 root 用户 `qingzhou` 运行，数据目录 `/data` 挂载为 volume。
- **健康检查**：容器 HEALTHCHECK 通过 wget 访问 `http://127.0.0.1:8081/` 验证服务就绪。
- **前端构建命令**：CI 明确使用 `npx vite build` 而非 `npm run build`，因为后者会先执行 vue-tsc 类型检查，而仓库存在历史类型错误，vite 构建仍能捕获真实编译错误。
- **安装脚本要求**：必须以 root 运行、需要 systemd、仅支持 Linux amd64/arm64；不支持的系统会提示改用 Docker 或源码构建。
- **sing-box 版本锁定**：release 流水线固定 `SB_VERSION=v1.13.18`，并验证产物包含 `with_v2ray_api`，否则构建失败——避免节点无法计量流量。
- **缓存策略**：Docker 构建使用 `cache-from/to: type=gha`；前端静态资源按 Vite 内容哈希命名，`assets/` 下文件设置 `Cache-Control: immutable`，`index.html` 强制 `no-store`。
- **环境变量驱动**：面板监听端口、数据库路径、探针目录均通过 `QZ_LISTEN`、`QZ_DB`、`QZ_PROBE_DIR` 等环境变量配置，`deploy/qingzhou.env.example` 提供模板。