#!/bin/bash
# One-click install script for qingzhou-probe agent.
# Usage: bash install.sh <panel_url> <probe_token>
#
# This script:
#   1. Downloads the probe binary for the current architecture
#   2. Creates a secure environment file (/etc/qingzhou-probe.env, mode 600)
#   3. Installs and starts a systemd service
set -e

PANEL_URL="${1%/}"  # trim trailing slash
TOKEN="$2"

if [ -z "$PANEL_URL" ] || [ -z "$TOKEN" ]; then
  echo "用法: bash install.sh <面板地址> <探针Token>"
  echo "示例: bash install.sh https://panel.example.com abc123token"
  exit 1
fi

ARCH=$(uname -m)
case $ARCH in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *) echo "不支持的架构: $ARCH"; exit 1 ;;
esac

BIN_URL="${PANEL_URL}/api/monitor/agent/linux-${ARCH}"
INSTALL_PATH="/usr/local/bin/qingzhou-probe"
ENV_FILE="/etc/qingzhou-probe.env"
SERVICE_FILE="/etc/systemd/system/qingzhou-probe.service"

echo "[1/4] 下载探针二进制 (${ARCH})..."
if command -v curl &> /dev/null; then
  HTTP_CODE=$(curl -sL -w "%{http_code}" -o "$INSTALL_PATH" "$BIN_URL")
  if [ "$HTTP_CODE" != "200" ]; then
    echo "❌ 下载失败: HTTP $HTTP_CODE"
    echo "   URL: $BIN_URL"
    exit 1
  fi
elif command -v wget &> /dev/null; then
  wget -qO "$INSTALL_PATH" "$BIN_URL" || { echo "❌ 下载失败"; exit 1; }
else
  echo "❌ 错误: 需要 curl 或 wget"
  exit 1
fi
chmod +x "$INSTALL_PATH"
echo "   已安装到 $INSTALL_PATH ($(du -h "$INSTALL_PATH" | cut -f1))"

# Create secure environment file (not visible in /proc/*/cmdline).
echo "[2/4] 创建环境配置文件..."
cat > "$ENV_FILE" << EOF
QZ_PROBE_SERVER=${PANEL_URL}
QZ_PROBE_TOKEN=${TOKEN}
EOF
chmod 600 "$ENV_FILE"

# Create systemd service.
echo "[3/4] 创建 systemd 服务..."
cat > "$SERVICE_FILE" << EOF
[Unit]
Description=Qingzhou Monitor Probe
After=network.target

[Service]
Type=simple
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_PATH}
Restart=always
RestartSec=10
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadOnlyPaths=/proc /sys

[Install]
WantedBy=multi-user.target
EOF

echo "[4/4] 启动服务..."
systemctl daemon-reload
systemctl enable qingzhou-probe &>/dev/null
systemctl restart qingzhou-probe

sleep 1
if systemctl is-active --quiet qingzhou-probe; then
  echo ""
  echo "✅ 探针安装完成！"
  echo "   服务状态: 运行中"
  echo "   查看日志: journalctl -u qingzhou-probe -f"
  echo "   卸载: systemctl disable --now qingzhou-probe && rm $INSTALL_PATH $ENV_FILE $SERVICE_FILE"
else
  echo ""
  echo "⚠️  服务启动失败，请检查日志:"
  echo "   journalctl -u qingzhou-probe -n 20 --no-pager"
  exit 1
fi
