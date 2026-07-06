# 部署 / 运维

## sing-box（每台落地机）
轻舟自管原生 sing-box。每台落地服务器先跑一键脚本（已装则检测、未装则装官方含
`v2ray_api` 版到 `/usr/local/bin/sing-box` + systemd + 内核调优，并输出可填入面板
「服务器」的信息）：
```bash
curl -fsSL https://<你的面板域名>/install-singbox.sh | bash
```
本机落地用脚本输出的默认值即可（`QZ_SINGBOX_*`）；远程落地在面板「服务器」新增并填写。

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
```bash
systemctl stop qingzhou        # 或直接覆盖后 restart
install -m755 qingzhou /opt/qingzhou/qingzhou
systemctl restart qingzhou
```

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
