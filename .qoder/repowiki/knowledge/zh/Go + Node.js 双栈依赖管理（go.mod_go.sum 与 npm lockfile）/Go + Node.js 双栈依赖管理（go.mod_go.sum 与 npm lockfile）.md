---
kind: dependency_management
name: Go + Node.js 双栈依赖管理（go.mod/go.sum 与 npm lockfile）
category: dependency_management
scope:
    - '**'
source_files:
    - go.mod
    - go.sum
    - frontend/package.json
    - frontend/package-lock.json
    - .github/workflows/ci.yml
    - Dockerfile
---

## 1. 使用的系统/工具

仓库采用**双栈依赖管理**：
- Go 后端使用 `go mod`，根目录的 `go.mod`、`go.sum` 为唯一声明源；未启用 vendor 目录，也未配置 GOPRIVATE 或私有代理。
- Vue 3 前端位于 `frontend/`，使用 `package.json` + `package-lock.json` 通过 npm 管理依赖。
- 构建与发布由 GitHub Actions（`.github/workflows/ci.yml`）和 Dockerfile 驱动，CI 中分别用 `setup-go@v5` 指定 Go 1.25、`setup-node@v4` 指定 Node 20，并执行 `npm ci` 与 `go build ./...`。

## 2. 关键文件

| 文件 | 作用 |
|---|---|
| `go.mod` | 模块名 `qingzhou`，Go 版本锁定 1.25.0，显式 require chi/v5、jwt/v5、uuid、x/crypto、x/net、yaml.v3、modernc.org/sqlite 等 |
| `go.sum` | Go 依赖校验摘要（随 go.mod 提交） |
| `frontend/package.json` | 前端运行时依赖（vue、pinia、naive-ui、echarts、qrcode 等）与开发依赖（vite、typescript、vue-tsc） |
| `frontend/package-lock.json` | npm 精确锁定的依赖树 |
| `.github/workflows/ci.yml` | CI 中固定 Go/Node 版本，前端走 `npm ci`，Go 走 `go build ./...` |
| `Dockerfile` | 多阶段构建：先 `node:20-alpine` 跑 `npm ci && npx vite build`，再 `golang:1.25-alpine` 交叉编译二进制，运行镜像基于 `alpine:3.20` |

## 3. 架构与约定

- **无 vendor 策略**：Go 依赖全部从公网（proxy.golang.org / github.com）拉取，不提交 vendor 目录。现代c/sqlite 以纯 Go 实现（CGO_ENABLED=0），避免 CGO 带来的平台差异。
- **严格版本锁定**：Go 侧通过 `go.sum` 锁定每个依赖的精确版本；前端通过 `package-lock.json` 锁定，CI 使用 `npm ci`（非 `npm install`）保证可重复安装。
- **最小化依赖**：Go 仅引入 6 个直接依赖，其余均为 indirect；前端仅引入 UI、图表、二维码等必要库，保持产物体积可控。
- **前端产物内嵌**：Dockerfile 第一阶段构建出 `frontend/dist`，第二阶段通过 `//go:embed all:dist` 将静态资源嵌入 Go 二进制，因此 CI 在 Go 构建前会生成一个占位 `index.html` 以避免完整 npm 安装。
- **探针二进制独立构建**：同一份 `cmd/probe/main.go` 在构建阶段以 linux/amd64 与 linux/arm64 各编译一份，随面板一起分发。

## 4. 约定与约束

- **Go 版本**：`go.mod` 顶部声明 `go 1.25.0`，CI 的 `setup-go` 也固定为 `1.25`，升级需同步修改两处。
- **前端 Node 版本**：CI 使用 `actions/setup-node@v4` 的 `node-version: "20"`，Dockerfile 同样基于 `node:20-alpine`，前后端构建环境一致。
- **不可变安装**：CI 对前端强制 `npm ci`，禁止交互式安装；Go 侧通过 `go.sum` 校验，任何未提交的变更都会导致构建失败。
- **无私有仓库/代理**：仓库未配置 `GOPRIVATE`、`GONOSUMDB`、`GONOPROXY` 或 `.gitconfig` 中的 git 凭据，所有 Go 依赖均从公开来源获取。
- **构建参数注入版本**：通过 `-ldflags "-X qingzhou/internal/version.Version=${VERSION}"` 注入版本号，供在线更新逻辑比较使用（见 `internal/updater` 相关代码）。
- **安全基线**：运行镜像仅安装 `ca-certificates`、`tzdata`、`wget`，并以非 root 用户 `qingzhou` 运行，减少攻击面。

综上，该仓库的依赖管理以“声明式清单 + 锁定文件 + CI 固定工具链”为核心，Go 与 Node 两条管线各自独立但通过 Docker 多阶段构建统一产出单一可执行镜像。