#!/usr/bin/env bash
# 轻舟面板 · sing-box 一键安装 / 检测脚本
#
#   curl -fsSL https://<你的面板域名>/install-singbox.sh | bash
#
# 作用：
#   1. 已装 sing-box → 直接检测并打印版本/路径（不重装，除非 --force）
#   2. 未装 → 安装官方最新版 sing-box（含 v2ray_api，面板统计依赖它）到
#      /usr/local/bin/sing-box，建 /etc/sing-box/config.json 占位配置 + systemd
#   3. 应用网络内核调优（BBR + fq + 缓冲区 + somaxconn 等）
#   4. 最后打印一段「服务器」信息，照着填进面板即可接管
#
# 选项： --force 强制重装   --no-tune 跳过内核调优
set -euo pipefail

BIN=/usr/local/bin/sing-box
CONF_DIR=/etc/sing-box
CONF=$CONF_DIR/config.json
UNIT=sing-box
V2RAY_LISTEN=127.0.0.1:18080
FORCE=0; TUNE=1
for a in "$@"; do case "$a" in --force) FORCE=1;; --no-tune) TUNE=0;; esac; done

c() { printf '\033[%sm%s\033[0m' "$1" "$2"; }
info() { echo "$(c '1;36' '›') $*"; }
ok()   { echo "$(c '1;32' '✓') $*"; }
warn() { echo "$(c '1;33' '!') $*"; }
die()  { echo "$(c '1;31' '✗') $*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "请用 root 运行（sudo bash）"

arch_tag() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64;;
    aarch64|arm64) echo arm64;;
    armv7l|armv7) echo armv7;;
    *) die "不支持的架构：$(uname -m)";;
  esac
}

detect_existing() {
  local found=""
  command -v sing-box >/dev/null 2>&1 && found="$(command -v sing-box)"
  [ -x "$BIN" ] && found="$BIN"
  [ -n "$found" ] || return 1
  echo "$found"
}

apply_tuning() {
  [ "$TUNE" = 1 ] || { warn "跳过内核调优（--no-tune）"; return; }
  info "应用网络内核调优 → /etc/sysctl.d/99-singbox.conf"
  cat >/etc/sysctl.d/99-singbox.conf <<'SYSCTL'
# 轻舟 · sing-box 网络调优
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 67108864
net.core.wmem_max = 67108864
net.ipv4.tcp_rmem = 4096 87380 67108864
net.ipv4.tcp_wmem = 4096 65536 67108864
net.core.somaxconn = 8192
net.core.netdev_max_backlog = 16384
net.ipv4.tcp_max_syn_backlog = 8192
net.ipv4.tcp_fastopen = 3
net.ipv4.tcp_mtu_probing = 1
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_notsent_lowat = 16384
net.ipv4.udp_rmem_min = 8192
net.ipv4.udp_wmem_min = 8192
net.ipv4.ip_forward = 1
fs.file-max = 1048576
SYSCTL
  sysctl --system >/dev/null 2>&1 || warn "sysctl --system 部分失败（可忽略）"
  # 提高打开文件数上限（QUIC/hy2 多连接）
  if ! grep -q 'singbox-nofile' /etc/security/limits.conf 2>/dev/null; then
    printf '* soft nofile 1048576\n* hard nofile 1048576\n# singbox-nofile\n' >>/etc/security/limits.conf
  fi
  local cc; cc=$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo '?')
  ok "内核调优完成（拥塞控制：$cc）"
}

install_singbox() {
  local arch tag ver url tmp
  arch=$(arch_tag)
  info "查询 sing-box 最新版本…"
  tag=$(curl -fsSL https://api.github.com/repos/SagerNet/sing-box/releases/latest \
        | grep -oE '"tag_name":\s*"v[^"]+"' | head -1 | grep -oE 'v[0-9.]+') \
        || die "无法获取版本（网络/GitHub 受限？可手动装后重跑本脚本检测）"
  ver=${tag#v}
  url="https://github.com/SagerNet/sing-box/releases/download/${tag}/sing-box-${ver}-linux-${arch}.tar.gz"
  info "下载 $tag ($arch)…"
  tmp=$(mktemp -d)
  curl -fL# "$url" -o "$tmp/sb.tgz" || die "下载失败：$url"
  tar -xzf "$tmp/sb.tgz" -C "$tmp"
  install -m755 "$tmp/sing-box-${ver}-linux-${arch}/sing-box" "$BIN"
  rm -rf "$tmp"
  ok "sing-box 已安装 → $BIN ($("$BIN" version | head -1))"
}

write_placeholder_conf() {
  mkdir -p "$CONF_DIR"
  if [ -s "$CONF" ] && "$BIN" check -c "$CONF" >/dev/null 2>&1; then
    info "已有可用配置 $CONF（保留，面板接管后会覆盖）"
    return
  fi
  info "写入占位配置 $CONF（面板接管后自动覆盖）"
  cat >"$CONF" <<'JSON'
{
  "log": { "level": "info", "timestamp": true },
  "inbounds": [],
  "outbounds": [ { "type": "direct", "tag": "direct" } ]
}
JSON
  "$BIN" check -c "$CONF" || die "占位配置校验失败"
}

write_unit() {
  info "配置 systemd 服务：$UNIT"
  cat >/etc/systemd/system/${UNIT}.service <<UNIT
[Unit]
Description=sing-box service (managed by 轻舟)
After=network.target nss-lookup.target
[Service]
ExecStart=$BIN run -c $CONF
Restart=on-failure
RestartSec=3s
LimitNOFILE=1048576
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable "$UNIT" >/dev/null 2>&1 || true
  systemctl restart "$UNIT"
  sleep 1
  systemctl is-active --quiet "$UNIT" && ok "$UNIT 运行中" || warn "$UNIT 未运行（systemctl status $UNIT 查看）"
}

public_ip() { curl -fsSL --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}'; }

summary() {
  local ip ver; ip=$(public_ip); ver=$("$BIN" version 2>/dev/null | head -1)
  echo
  echo "$(c '1;32' '════════ 安装完成，请到面板「服务器」处填写 ════════')"
  cat <<TXT
  服务器地址(host)   : ${ip:-<你的服务器IP>}
  SSH 端口           : 22                （如改过请填实际端口）
  SSH 用户           : root
  sing-box 二进制     : $BIN
  配置路径(config)   : $CONF
  systemd 服务名      : $UNIT
  v2ray_api 监听      : $V2RAY_LISTEN     （统计用，面板自动写入，无需改）
  版本               : $ver
TXT
  echo "$(c '1;32' '═══════════════════════════════════════════════════')"
  echo "提示：本机面板可直接用以上默认值；远程落地机需在面板新增「服务器」并填写。"
}

main() {
  if cur=$(detect_existing) && [ "$FORCE" = 0 ]; then
    ok "检测到已安装 sing-box：$cur （$("$cur" version 2>/dev/null | head -1)）"
    info "如需重装：在命令后加 --force"
    [ "$cur" = "$BIN" ] || warn "注意：面板默认二进制路径是 $BIN，当前为 $cur，填面板时请用实际路径"
    apply_tuning
    write_placeholder_conf
    write_unit
    summary
    exit 0
  fi
  install_singbox
  apply_tuning
  write_placeholder_conf
  write_unit
  summary
}
main
