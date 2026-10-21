package store

// Mirror profile precedence over inline options without treating tls_id=0 as
// proof of plaintext. Unknown inline shapes are omitted from public export;
// they must never be advertised with security=none by accident.
//
// 品牌定制（CF 前置中转）：服务端 profile enabled:false 表示入站监听明文（TLS
// 由前置的 Cloudflare/nginx 终结）——这不代表客户端到前置代理也是明文。client
// 侧 profile 显式 enabled:true，或携带 server_name（必然是 TLS 握手的 SNI）时，
// 分享链接仍按 TLS 渲染，客户端才会用 TLS 打前置代理的 443。否则上游会把
// 这类入站渲染成 security=none，明文 ws 打 CF 443 直接 400。
func shareLinkTLSState(tlsID int64, profile, client, options map[string]interface{}) (enabled, disabled bool, server map[string]interface{}, valid bool) {
	if tlsID != 0 {
		if value, ok := profile["enabled"].(bool); ok && !value {
			if c, ok := client["enabled"].(bool); ok && c {
				return true, false, profile, true
			}
			if sn := mapStr(client, "server_name"); sn != "" {
				return true, false, profile, true
			}
			return false, true, profile, true
		}
		return true, false, profile, true // Unresolved profiles must never cause a plaintext downgrade
	}
	raw, exists := options["tls"]
	if !exists || raw == nil {
		return false, true, nil, true
	}
	inline, ok := raw.(map[string]interface{})
	if !ok {
		return false, false, nil, false
	}
	value, ok := inline["enabled"].(bool)
	if !ok {
		return false, false, nil, false
	}
	return value, !value, inline, true
}
