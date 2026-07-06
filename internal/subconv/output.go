package subconv

import (
	"encoding/base64"
	"strings"
)

// Format constants.
const (
	FormatBase64  = "base64"
	FormatClash   = "clash"
	FormatSingbox = "singbox"
	FormatSurge   = "surge"
)

// NormalizeFormat maps user-facing format names to canonical ones.
func NormalizeFormat(f string) string {
	switch strings.ToLower(f) {
	case "clash", "mihomo", "meta":
		return FormatClash
	case "singbox", "sing-box", "json":
		return FormatSingbox
	case "surge":
		return FormatSurge
	default:
		return FormatBase64
	}
}

// FormatForUA picks a sensible subscription format from a client's User-Agent,
// so users importing into Clash/sing-box/Surge get a native config without
// having to append ?format=. Anything unrecognised (v2rayN, Shadowrocket,
// NekoBox, Quantumult, …) falls back to the universal base64 link list.
func FormatForUA(ua string) string {
	s := strings.ToLower(ua)
	switch {
	case strings.Contains(s, "clash") || strings.Contains(s, "mihomo") || strings.Contains(s, "stash"):
		return FormatClash
	case strings.Contains(s, "sing-box") || strings.Contains(s, "singbox"):
		return FormatSingbox
	case strings.Contains(s, "surge"):
		return FormatSurge
	default:
		return FormatBase64
	}
}

// Base64 joins raw share links and base64-encodes them (the universal format).
func Base64(links []string) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
}

// Render produces a subscription body for the given format. clashTpl/singboxTpl
// may be empty to use the built-in anti-leak templates. groups maps a share link
// to the panel node-group it belongs to (drives the per-group auto-select
// groups); may be nil. subURL, if set, is the subscription's own URL (used by
// Surge's MANAGED-CONFIG auto-update header).
func Render(format string, links []string, groups map[string]string, clashTpl, singboxTpl, subURL string) (body string, contentType string, err error) {
	switch NormalizeFormat(format) {
	case FormatClash:
		out, e := Clash(parseWithGroups(links, groups), clashTpl)
		return out, "text/yaml; charset=utf-8", e
	case FormatSingbox:
		out, e := Singbox(parseWithGroups(links, groups), singboxTpl)
		return out, "application/json; charset=utf-8", e
	case FormatSurge:
		return Surge(parseWithGroups(links, groups), subURL), "text/plain; charset=utf-8", nil
	default:
		return Base64(links), "text/plain; charset=utf-8", nil
	}
}

// parseWithGroups parses links and tags each proxy with its panel node-group.
func parseWithGroups(links []string, groups map[string]string) []*Proxy {
	ps := ParseLinks(links)
	if len(groups) > 0 {
		for _, p := range ps {
			p.Group = groups[p.Raw]
		}
	}
	return ps
}

// ParseLinks parses each link, skipping unparseable ones.
func ParseLinks(links []string) []*Proxy {
	var out []*Proxy
	for _, l := range links {
		if p, err := ParseLink(l); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// DefaultClashTemplate carries the anti-leak config (fake-ip DNS + sniffer +
// TUN + rules). The TUN section includes route-exclude-address with private
// ranges; proxy server IPs are dynamically injected by injectRouteExclude()
// at render time — this is server-side data (the nodes the server defines),
// not client-side configuration.
const DefaultClashTemplate = `
dns:
  enable: true
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-filter:
    - "*.lan"
    - "*.local"
    - "+.msftconnecttest.com"
    - "+.msftncsi.com"
    - "time.*.com"
    - "ntp.*.com"
    - "+.pool.ntp.org"
    - "+.stun.*.*"
    - "+.stun.*.*.*"
    - "+.stun.*.*.*.*"
    - "+.stun.*.*.*.*.*"
  ipv6: false
  default-nameserver:
    - 223.5.5.5
    - 119.29.29.29
  nameserver:
    - 223.5.5.5
    - 119.29.29.29
    - https://doh.pub/dns-query
    - https://dns.alidns.com/dns-query
  fallback:
    - https://1.1.1.1/dns-query
    - https://dns.cloudflare.com/dns-query
    - tls://1.1.1.1:853
    - tls://8.8.8.8:853
  fallback-filter:
    geoip: true
    geoip-code: CN
    ipcidr:
      - 240.0.0.0/4
      - 0.0.0.0/32
  proxy-server-nameserver:
    - 223.5.5.5
    - 119.29.29.29
sniffer:
  enable: true
  sniff:
    TLS:
      ports: [443, 8443]
    HTTP:
      ports: [80, 8080-8880]
    QUIC:
      ports: [443]
tun:
  enable: true
  stack: gvisor
  auto-route: true
  auto-detect-interface: true
  dns-hijack:
    - "any:53"
  strict-route: false
  route-exclude-address:
    - 192.168.0.0/16
    - 10.0.0.0/8
    - 172.16.0.0/12
    - 169.254.0.0/16
    - 224.0.0.0/4
    - 255.255.255.255/32
rules:
  - GEOSITE,category-ads-all,REJECT
  - GEOSITE,private,DIRECT
  - GEOIP,private,DIRECT,no-resolve
  - GEOSITE,cn,DIRECT
  - GEOIP,CN,DIRECT
`

// DefaultSingboxTemplate carries the sing-box anti-leak dns+route with built-in
// routing: CN domains resolve via local DNS and go direct, ads are rejected,
// everything else is fake-ip'd through the proxy. Rule-sets are fetched once and
// cached. Targets sing-box ≥1.11.
const DefaultSingboxTemplate = `{
  "dns": {
    "servers": [
      {"tag": "remote", "address": "https://1.1.1.1/dns-query", "detour": "proxy"},
      {"tag": "local", "address": "https://223.5.5.5/dns-query", "detour": "direct"},
      {"tag": "fake", "address": "fakeip"}
    ],
    "rules": [
      {"rule_set": "geosite-cn", "server": "local"},
      {"query_type": ["A", "AAAA"], "server": "fake"}
    ],
    "fakeip": {"enabled": true, "inet4_range": "198.18.0.0/15", "inet6_range": "fc00::/18"},
    "independent_cache": true,
    "final": "remote"
  },
  "route": {
    "auto_detect_interface": true,
    "final": "proxy",
    "rules": [
      {"action": "sniff"},
      {"protocol": "dns", "action": "hijack-dns"},
      {"rule_set": "geosite-ads", "action": "reject"},
      {"ip_is_private": true, "outbound": "direct"},
      {"rule_set": ["geoip-cn", "geosite-cn"], "outbound": "direct"}
    ],
    "rule_set": [
      {"type": "remote", "tag": "geosite-ads", "format": "binary", "download_detour": "proxy",
       "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ads-all.srs"},
      {"type": "remote", "tag": "geosite-cn", "format": "binary", "download_detour": "proxy",
       "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-geolocation-cn.srs"},
      {"type": "remote", "tag": "geoip-cn", "format": "binary", "download_detour": "proxy",
       "url": "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs"}
    ]
  },
  "experimental": {
    "cache_file": {"enabled": true, "store_rdrc": true}
  }
}`
