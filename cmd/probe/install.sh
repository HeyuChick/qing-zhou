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
# 下载到同目录临时文件，成功后原子替换。直接覆盖正在运行的二进制会触发
# ETXTBSY(Text file busy)，导致 curl 写入失败(退出码 23)——升级场景必现。
TMP_PATH="${INSTALL_PATH}.new"
trap 'rm -f "$TMP_PATH"' EXIT
if command -v curl &> /dev/null; then
<<<<<<< Updated upstream
<<<<<<< Updated upstream
  CURL_RC=0
  HTTP_CODE=$(curl -sSL -w "%{http_code}" -o "$TMP_PATH" "$BIN_URL") || CURL_RC=$?
  if [ "$CURL_RC" != "0" ]; then
    echo "❌ 下载失败: curl 退出码 $CURL_RC"
    echo "   URL: $BIN_URL"
=======
=======
>>>>>>> Stashed changes
  set +e
  HTTP_CODE=$(curl -sL -w "%{http_code}" -o "$INSTALL_PATH" "$BIN_URL" 2>&1)
  CURL_EXIT=$?
  set -e
  if [ $CURL_EXIT -ne 0 ]; then
    echo "❌ 下载失败: curl 退出码 $CURL_EXIT"
    echo "   URL: $BIN_URL"
    echo "   请检查网络连接和DNS解析"
<<<<<<< Updated upstream
>>>>>>> Stashed changes
=======
>>>>>>> Stashed changes
    exit 1
  fi
  if [ "$HTTP_CODE" != "200" ]; then
    echo "❌ 下载失败: HTTP $HTTP_CODE"
    echo "   URL: $BIN_URL"
    exit 1
  fi
elif command -v wget &> /dev/null; then
<<<<<<< Updated upstream
<<<<<<< Updated upstream
  wget -qO "$TMP_PATH" "$BIN_URL" || { echo "❌ 下载失败"; exit 1; }
=======
=======
>>>>>>> Stashed changes
  set +e
  wget -qO "$INSTALL_PATH" "$BIN_URL" 2>&1
  WGET_EXIT=$?
  set -e
  if [ $WGET_EXIT -ne 0 ]; then
    echo "❌ 下载失败: wget 退出码 $WGET_EXIT"
    echo "   URL: $BIN_URL"
    exit 1
  fi
<<<<<<< Updated upstream
>>>>>>> Stashed changes
=======
>>>>>>> Stashed changes
else
  echo "❌ 错误: 需要 curl 或 wget"
  exit 1
fi
if [ ! -s "$TMP_PATH" ]; then
  echo "❌ 下载的文件为空，请检查 URL 是否正确"
  echo "   URL: $BIN_URL"
  exit 1
fi
chmod +x "$TMP_PATH"
# 原子替换：rename() 对正在运行的旧二进制是安全的，不会 ETXTBSY。
mv -f "$TMP_PATH" "$INSTALL_PATH"
trap - EXIT
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
