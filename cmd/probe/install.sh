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

echo "下载探针二进制 (${ARCH})..."
if command -v curl &> /dev/null; then
  curl -sL "$BIN_URL" -o "$INSTALL_PATH"
elif command -v wget &> /dev/null; then
  wget -qO "$INSTALL_PATH" "$BIN_URL"
else
  echo "错误: 需要 curl 或 wget"
  exit 1
fi
chmod +x "$INSTALL_PATH"

# Create secure environment file (not visible in /proc/*/cmdline).
echo "创建环境配置文件..."
cat > "$ENV_FILE" << EOF
QZ_PROBE_SERVER=${PANEL_URL}
QZ_PROBE_TOKEN=${TOKEN}
EOF
chmod 600 "$ENV_FILE"

# Create systemd service.
echo "创建 systemd 服务..."
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

systemctl daemon-reload
systemctl enable --now qingzhou-probe

echo ""
echo "✅ 探针安装完成！"
echo "   服务状态: systemctl status qingzhou-probe"
echo "   查看日志: journalctl -u qingzhou-probe -f"
echo "   卸载: systemctl disable --now qingzhou-probe && rm $INSTALL_PATH $ENV_FILE $SERVICE_FILE"
