# 部署 / 运维

## Docker（最省事）

面板是中心机、SSH 管远程落地，容器不需要跑 sing-box，只需持久化 DB。镜像内置两架构探针，支持 amd64/arm64。

```bash
# 改 docker-compose.yml 里的 QZ_PUBLIC_BASE 与 QZ_SECRET_KEY(openssl rand -hex 32)
docker compose up -d
docker compose logs -f qingzhou     # 首启打印随机管理员密码（未设 QZ_ADMIN_PASS 时）
```

要点：容器内 `QZ_LISTEN=0.0.0.0:8081`（镜像默认）；DB 在 `/data`（挂卷持久化）；生产必设 `QZ_SECRET_KEY`；
以 uid 10001 非 root 运行（命名卷可直接写，bind 挂载需 `chown 10001`）。**升级用「拉新镜像 + 重建容器」，
不要用面板内在线更新。** 详见 Wiki「Docker 部署」。自建镜像 `docker build --build-arg VERSION=<tag> -t qingzhou .`；
发 release 时 `.github/workflows/docker.yml` 会自动构建并推送到 GHCR。

## sing-box（每台落地机）
轻舟自管原生 sing-box。每台落地服务器先跑一键脚本（已装则检测、未装则装官方含
`v2ray_api` 版到 `/usr/local/bin/sing-box` + systemd + 内核调优，并输出可填入面板
「服务器」的信息）：
```bash
curl -fsSL https://<你的面板域名>/install-singbox.sh | bash
```
面板「**系统设置 → 面板访问地址**」会按你配置的地址生成可一键复制的完整命令。访问地址来源优先级：
`QZ_PUBLIC_BASE` 环境变量 > 设置页「访问地址」> 反代头/请求 Host。

本机落地用脚本输出的默认值即可（`QZ_SINGBOX_*`）；远程落地在面板「服务器」新增并填写。

## 探针监控（可选，每台服务器）
探针 `qingzhou-probe` 上报 CPU/内存/磁盘/负载，安装命令在面板「服务器监控」页获取。

> **注意**：探针二进制由面板托管下载（`/api/monitor/agent/linux-<arch>`），从 `QZ_PROBE_DIR`
> 指定的目录读取，默认相对路径 `cmd/probe/dist`。二进制部署时该相对目录通常不存在，探针安装会
> 报「下载失败: HTTP 404」。请从 GitHub Release 下载 `probe-linux-amd64` / `probe-linux-arm64`
> 放到一个目录（文件名不变），并在 env 里设 `QZ_PROBE_DIR=/opt/qingzhou/probe`。

## 目录约定（服务器）
- 程序：`/opt/qingzhou/qingzhou`
- 配置：`/opt/qingzhou/qingzhou.env`（`chmod 600`，见 `qingzhou.env.example`）
- 数据库：`/opt/qingzhou/qingzhou.db`（WAL 模式）
- 备份：`/opt/qingzhou/backups/`

## 安装
```bash
# 1. 二进制 + 配置
install -m755 qingzhou /opt/qingzhou/qingzhou
install -m600 qingzhou.env /opt/qingzhou/qingzhou.env   # 按 env.example 填好

# 2. systemd
cp deploy/qingzhou.service /etc/systemd/system/
systemctl daemon-reload && systemctl enable --now qingzhou

# 3. nginx 反代到 127.0.0.1:8081（HTTPS 证书用 certbot），略
```

## 更新

### 面板内在线更新（推荐）
管理后台 →「在线更新」页可一键升级：面板读取 GitHub 最新 release，显示变更日志，
点「立即更新」后自动**下载对应架构二进制 → 校验 SHA-256 → 原子替换 → 进程自我重启**
（同 PID，兼容 `Restart=on-failure`，约 1~2 秒短暂中断）。要能生效，二进制必须带内置
版本号（见下方构建说明），且发布产物里有 `qingzhou-linux-<arch>` 资产——`.github/workflows/release.yml`
会在你于 GitHub 上发布 release 时自动构建并上传（版本号自动注入 = release tag）。

> 仅 Linux 部署支持自更新；其他平台请手动替换。可选环境变量：
> `QZ_UPDATE_REPO`（默认 `mllt992/qing-zhou`）、`QZ_UPDATE_GITHUB_TOKEN`（提升 GitHub API 速率上限）。

### 手动更新
```bash
systemctl stop qingzhou        # 或直接覆盖后 restart
install -m755 qingzhou /opt/qingzhou/qingzhou
systemctl restart qingzhou
```

### 从源码构建（必须注入版本号，否则在线更新无法比较版本）
```bash
# 1. 构建内嵌前端
cd frontend && npm ci && npx vite build && cd ..
# 2. 注入版本号（= 目标 release tag）构建面板
go build -ldflags "-s -w -X qingzhou/internal/version.Version=v0.2.2" -o qingzhou .
```
不带 `-X ...version.Version` 时版本为 `dev`，在线更新页会把任意最新 release 视为「可更新」。

## 数据库备份（每日）
```bash
apt-get install -y sqlite3
install -m755 deploy/backup.sh /opt/qingzhou/backup.sh
# cron：每天 04:30，保留 7 天
cat > /etc/cron.d/qingzhou-backup <<'CRON'
30 4 * * * root /opt/qingzhou/backup.sh >> /opt/qingzhou/backups/backup.log 2>&1
CRON
/opt/qingzhou/backup.sh    # 立即跑一次验证
```
`backup.sh` 用 `sqlite3 .backup` 在线热备份 `qingzhou.db`，运行中执行安全。

## 密钥
`QZ_SECRET_KEY`（env 内，DB 外）用于加密 SMTP / Reality 私钥等敏感配置落库。生成：`openssl rand -hex 32`。
一旦设置并写入了加密配置，请勿更换该值，否则已加密内容无法解密。
