// Package singbox generates a sing-box server config.json from 轻舟's own data
// (B2 integration model: 轻舟 generates the config, sing-box runs as a separate
// process, per-user stats are read back over the v2ray_api gRPC StatsService).
//
// The assembly model: each inbound carries a pre-rendered sing-box JSON body;
// active users are
// injected into its `users[]`; every user name is also listed under
// experimental.v2ray_api.stats.users so the StatsService tracks it per user.
package singbox

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"sort"
)

// DefaultBaseConfig is the log/dns/route/outbounds template used when the admin
// hasn't set a custom sb_base_config. Inbounds/experimental are filled by
// GenerateConfig.
//
// No legacy domain_strategy / dns.strategy here: sing-box ≥1.12 deprecated those
// "domain strategy options" and ≥1.14 removes them, making `sing-box check` fail
// fatally. Modern sing-box dials with happy-eyeballs (concurrent A/AAAA), so the
// old prefer_ipv4 hint that avoided AAAA-stall on IPv6-broken VPS is no longer
// needed. GenerateConfig also strips these fields from any custom sb_base_config
// override, so configs stay valid across server versions.
const DefaultBaseConfig = `{
  "log": {"level": "warn", "timestamp": true},
  "dns": {"servers": [{"type": "local", "tag": "local"}]},
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"rules": [], "final": "direct"}
}`

// User is one 轻舟 user's proxy credentials. A single user can appear in several
// inbounds; each inbound renders only the fields its protocol needs.
type User struct {
	Name     string // identity used in users[] AND as the v2ray stats key
	UUID     string // vless / vmess / tuic
	Password string // hysteria2 / tuic / trojan
}

// Inbound is one sing-box inbound: its protocol Type, the pre-rendered body
// (type/tag/listen_port/tls/transport/...) as Base, and the users entitled to it.
type Inbound struct {
	Type  string
	Base  map[string]interface{}
	Users []User
}

// renderUser maps a 轻舟 user to the protocol-specific user object sing-box
// expects. hasTLS/hasTransport gate the VLESS flow: xtls-rprx-vision is only
// valid on raw-TLS (Reality) VLESS — it must be dropped when there is no TLS or
// when a transport (ws/grpc/...) is present (matches sing-box's stripVision rule).
func renderUser(t string, u User, ib map[string]interface{}) map[string]interface{} {
	_, hasTLS := ib["tls"]
	hasTransport := false
	if tr, ok := ib["transport"].(map[string]interface{}); ok && len(tr) > 0 {
		hasTransport = true
	}
	switch t {
	case "vless":
		m := map[string]interface{}{"name": u.Name, "uuid": u.UUID}
		if hasTLS && !hasTransport {
			m["flow"] = "xtls-rprx-vision"
		}
		return m
	case "vmess":
		return map[string]interface{}{"name": u.Name, "uuid": u.UUID, "alterId": 0}
	case "tuic":
		return map[string]interface{}{"name": u.Name, "uuid": u.UUID, "password": u.Password}
	case "hysteria2":
		return map[string]interface{}{"name": u.Name, "password": u.Password}
	case "trojan":
		return map[string]interface{}{"name": u.Name, "password": u.Password}
	case "shadowsocks":
		method, _ := ib["method"].(string)
		return map[string]interface{}{"name": u.Name, "password": DeriveSSKey(u.Password, method)}
	case "anytls":
		return map[string]interface{}{"name": u.Name, "password": u.Password}
	case "hysteria":
		return map[string]interface{}{"name": u.Name, "auth_str": u.Password}
	default:
		return map[string]interface{}{"name": u.Name}
	}
}

// SSKeyLen returns the byte length of the per-user key for a shadowsocks 2022
// method (the only family that supports multi-user). 0 for unknown.
func SSKeyLen(method string) int {
	switch method {
	case "2022-blake3-aes-128-gcm":
		return 16
	case "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
		return 32
	}
	return 32
}

// DeriveSSKey deterministically derives a valid base64 shadowsocks-2022 key of
// the method's required length from the user's secret, so 轻舟's single
// per-user secret maps cleanly onto SS without storing extra credentials.
func DeriveSSKey(secret, method string) string {
	h := sha256.Sum256([]byte("qz-ss:" + secret))
	return base64.StdEncoding.EncodeToString(h[:SSKeyLen(method)])
}

// GenerateConfig assembles the full sing-box config: it starts from base (the
// log/dns/route/ntp/outbounds template), appends the inbounds with users
// injected, and wires experimental.v2ray_api.stats to track every user.
// v2rayListen is the gRPC listen address (default 127.0.0.1:18080).
func GenerateConfig(base json.RawMessage, inbounds []Inbound, v2rayListen string) ([]byte, error) {
	cfg := map[string]interface{}{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &cfg); err != nil {
			return nil, err
		}
	}

	names := []string{}
	seen := map[string]bool{}
	ibList := []interface{}{}
	for _, ib := range inbounds {
		m := map[string]interface{}{}
		for k, v := range ib.Base {
			m[k] = v
		}
		// An empty transport object (sing-box stores `"transport": {}`) is NOT a real
		// transport: drop it so it doesn't strip the VLESS Reality vision flow and
		// doesn't confuse sing-box at runtime.
		if tr, ok := m["transport"].(map[string]interface{}); ok && len(tr) == 0 {
			delete(m, "transport")
		}
		// Sort users by name so the generated config is byte-deterministic
		// (callers may pass users in nondeterministic DB order); this lets the
		// process manager skip reloads when nothing actually changed.
		su := append([]User(nil), ib.Users...)
		sort.Slice(su, func(i, j int) bool { return su[i].Name < su[j].Name })
		users := []interface{}{}
		for _, u := range su {
			users = append(users, renderUser(ib.Type, u, m))
			if !seen[u.Name] {
				seen[u.Name] = true
				names = append(names, u.Name)
			}
		}
		m["users"] = users
		ibList = append(ibList, m)
	}
	cfg["inbounds"] = ibList
	sort.Strings(names)

	// v2rayListen="" means skip v2ray_api (used for remote servers without the plugin).
	if v2rayListen != "" {
		exp, _ := cfg["experimental"].(map[string]interface{})
		if exp == nil {
			exp = map[string]interface{}{}
		}
		exp["v2ray_api"] = map[string]interface{}{
			"listen": v2rayListen,
			"stats":  map[string]interface{}{"enabled": true, "users": names},
		}
		cfg["experimental"] = exp
	}

	stripLegacyDomainStrategy(cfg)
	return json.MarshalIndent(cfg, "", "  ")
}

// stripLegacyDomainStrategy removes the pre-1.12 "domain strategy" knobs that
// sing-box ≥1.12 rejects on `sing-box check` (FATAL unless
// ENABLE_DEPRECATED_LEGACY_DOMAIN_STRATEGY_OPTIONS=true): the global dns.strategy
// and per-inbound/outbound/route-rule domain_strategy. They were optional
// dial/resolve hints; modern sing-box does happy-eyeballs by default, so dropping
// them is safe and keeps the generated config valid whatever a server's version.
// Applied to the fully-assembled config so it covers a custom sb_base_config
// override too (which we may not control).
func stripLegacyDomainStrategy(cfg map[string]interface{}) {
	if dns, ok := cfg["dns"].(map[string]interface{}); ok {
		delete(dns, "strategy")
	}
	for _, key := range []string{"outbounds", "inbounds"} {
		if list, ok := cfg[key].([]interface{}); ok {
			for _, it := range list {
				if m, ok := it.(map[string]interface{}); ok {
					delete(m, "domain_strategy")
				}
			}
		}
	}
	if route, ok := cfg["route"].(map[string]interface{}); ok {
		if rules, ok := route["rules"].([]interface{}); ok {
			for _, it := range rules {
				if m, ok := it.(map[string]interface{}); ok {
					delete(m, "domain_strategy")
				}
			}
		}
	}
}
