package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"qingzhou/internal/singbox"
)

// A keyed digest freezes the exact authentication object without retaining an
// additional plaintext copy of user-selected passwords. Protocol, UUID, flow,
// SS2022 derived key and TUIC's two-part credential are all covered.
func (s *Store) relayAuthenticationHash(user map[string]interface{}) string {
	raw, err := json.Marshal(user)
	if err != nil || user == nil {
		return ""
	}
	digest := hmac.New(sha256.New, s.secretKey)
	digest.Write([]byte("qingzhou-relay-source-auth-v1\x00"))
	digest.Write(raw)
	return hex.EncodeToString(digest.Sum(nil))
}

// Resolve the listener from the same transaction that acknowledges the apply.
// All TLS/certificate lookups populate caches before resolveTlsBlock, so no
// nested connection can see a different desired state or deadlock SQLite.
func (s *Store) relayMeteringInboundBaseWith(db txLike, ib *SbInbound) (map[string]interface{}, error) {
	base := map[string]interface{}{
		"type": ib.Type, "tag": ib.Tag, "listen": ib.Listen, "listen_port": ib.ListenPort,
	}
	if ib.Options != "" {
		var opts map[string]interface{}
		if err := json.Unmarshal([]byte(ib.Options), &opts); err != nil {
			return nil, fmt.Errorf("入站 %s 的配置无效: %w", ib.Tag, err)
		}
		for key, value := range opts {
			base[key] = value
		}
	}
	if ib.TlsID != 0 {
		_, _, tls, err := s.relayTargetSpecWith(db, ib)
		if err != nil {
			return nil, err
		}
		certs := map[int64]*Cert{}
		if tls != nil && tls.CertID != 0 {
			cert, err := s.scanCert(db.QueryRow(`SELECT `+certCols+` FROM certificates WHERE id=?`, tls.CertID))
			if err != nil {
				return nil, fmt.Errorf("入站 %s 的证书不可用: %w", ib.Tag, err)
			}
			certs[tls.CertID] = cert
		}
		block, err := s.resolveTlsBlock(ib.TlsID, ib.Tag, map[int64]*SbTls{ib.TlsID: tls}, certs)
		if err != nil {
			return nil, err
		}
		if block != nil {
			base["tls"] = block
		}
	}
	return base, nil
}

// Use the production inbound renderer for its normalization and client-only
// option stripping. A placeholder makes mixed render; it is removed below and
// is never sent to a core. This keeps apply verification in sync with rendering.
func relayMeteringRenderedInboundBase(ib *SbInbound, base map[string]interface{}) (map[string]interface{}, error) {
	raw, err := singbox.GenerateConfig(nil, []singbox.Inbound{{Type: ib.Type, Base: base,
		Users: []singbox.User{{Name: "verification-only"}}}}, "")
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Inbounds []map[string]interface{} `json:"inbounds"`
	}
	if err = json.Unmarshal(raw, &cfg); err != nil || len(cfg.Inbounds) != 1 {
		return nil, fmt.Errorf("入站 %s 的验证配置无法生成", ib.Tag)
	}
	delete(cfg.Inbounds[0], "users")
	return cfg.Inbounds[0], nil
}

// Duplicate wire credentials can authenticate as the last user's name even
// when both expected objects are present. Compare the protocol's actual lookup
// key, not a (name,password) tuple: e.g. TUIC indexes UUID before password and
// mixed indexes username, so equal passwords for distinct mixed users are fine.
func relayAuthenticationKey(protocol string, user map[string]interface{}) string {
	text := func(key string) string { value, _ := user[key].(string); return value }
	switch protocol {
	case "vless", "vmess", "tuic":
		value := text("uuid")
		if value == "" {
			return ""
		}
		id, err := uuid.Parse(value)
		if err != nil {
			if protocol == "tuic" {
				return ""
			}
			id = uuid.NewSHA1(uuid.Nil, []byte(value))
		}
		return "uuid:" + id.String()
	case "mixed":
		if value := text("username"); value != "" {
			return "username:" + value
		}
	case "hysteria":
		if value := text("auth_str"); value != "" {
			return "auth:" + value
		}
		if value, err := base64.StdEncoding.DecodeString(text("auth")); err == nil && len(value) != 0 {
			return "auth:" + string(value)
		}
	case "shadowsocks":
		value, err := base64.StdEncoding.DecodeString(text("password"))
		if err != nil {
			value, err = base64.RawStdEncoding.DecodeString(text("password"))
		}
		if err == nil && len(value) != 0 {
			return "ss:" + base64.StdEncoding.EncodeToString(value)
		}
	case "trojan", "anytls", "hysteria2":
		if value := text("password"); value != "" {
			return "password:" + value
		}
	}
	return ""
}
