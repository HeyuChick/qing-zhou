package api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/acmesh"
	"qingzhou/internal/sbproc"
	"qingzhou/internal/singbox"
	"qingzhou/internal/sshctl"
	"qingzhou/internal/store"
)

// ===== native sing-box (B2) admin: TLS/Reality profiles & inbounds =====

// POST /api/admin/sb/reality-keypair — fresh x25519 keypair + short_id.
func (a *API) handleAdminRealityKeypair(w http.ResponseWriter, r *http.Request) {
	priv, pub, err := singbox.GenerateRealityKeypair()
	if err != nil {
		fail(w, http.StatusInternalServerError, "生成密钥失败")
		return
	}
	sid, _ := singbox.GenerateShortID(8)
	ok(w, J{"private_key": priv, "public_key": pub, "short_id": sid})
}

// POST /api/admin/sb/tls/self-signed {server_name, days?} — generate a
// self-signed certificate + key for the given SNI, returned as PEM. Used by the
// 证书 TLS drawer's one-click button; the operator still reviews and saves.
func (a *API) handleAdminSelfSignedCert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerName string `json:"server_name"`
		Days       int    `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if strings.TrimSpace(req.ServerName) == "" {
		fail(w, http.StatusBadRequest, "SNI 域名必填")
		return
	}
	certPEM, keyPEM, err := singbox.GenerateSelfSignedCert(req.ServerName, req.Days)
	if err != nil {
		fail(w, http.StatusInternalServerError, "生成自签证书失败："+err.Error())
		return
	}
	ok(w, J{"certificate": certPEM, "key": keyPEM})
}

// handleAdminQuickSelfSignedTls generates a self-signed cert AND saves it as a
// ready-to-bind TLS entry in one call — the "一键绑定证书" path for mixed
// (HTTP/SOCKS5) inbounds that want to become HTTPS proxies without a domain. The
// SNI defaults to the address clients dial for the inbound's server (its host,
// else the panel's node host), so the cert's SAN matches; it also embeds an IP
// SAN when that address is an IP. Returns the saved TLS entry (with its id).
func (a *API) handleAdminQuickSelfSignedTls(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerID   int64  `json:"server_id"`
		ServerName string `json:"server_name"` // optional override (domain or IP)
		Name       string `json:"name"`        // optional TLS entry name
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	sni := strings.TrimSpace(req.ServerName)
	if sni == "" {
		if req.ServerID != 0 {
			if sv, _ := a.st.GetServer(req.ServerID); sv != nil {
				sni = strings.TrimSpace(sv.Host)
			}
		}
		if sni == "" {
			sni = strings.TrimSpace(a.nodeHost())
		}
	}
	if sni == "" {
		fail(w, http.StatusBadRequest, "无法确定证书域名/IP：请在该服务器或「系统设置 → 面板访问地址」配置 host，或手动填写 SNI")
		return
	}
	certPEM, keyPEM, err := singbox.GenerateSelfSignedCert(sni, 3650)
	if err != nil {
		fail(w, http.StatusInternalServerError, "生成自签证书失败："+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "自签-" + sni
	}
	server := map[string]interface{}{
		"enabled":     true,
		"server_name": sni,
		"certificate": certPEM,
		"key":         keyPEM,
	}
	// insecure=true on the client side: a self-signed cert won't chain to a public
	// CA, so clients (and 轻舟's own link renderers) must skip verification.
	client := map[string]interface{}{
		"insecure": true,
		"utls":     map[string]interface{}{"enabled": true, "fingerprint": "chrome"},
	}
	sj, _ := json.Marshal(server)
	cj, _ := json.Marshal(client)
	newID, err := a.st.SaveSbTls(&store.SbTls{ServerID: req.ServerID, Name: name, Mode: "tls", ServerJSON: string(sj), ClientJSON: string(cj)})
	if err != nil {
		fail(w, http.StatusInternalServerError, "保存证书失败")
		return
	}
	saved, _ := a.st.GetSbTls(newID)
	ok(w, saved)
}

// sbSetting resolves a sing-box config value the same way the controller does:
// env var → DB setting → default. Keeps the ACME reload command and cert dir in
// sync with where the local sing-box actually reads its config / systemd unit.
func (a *API) sbSetting(envKey, settingKey, def string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	if v, _ := a.st.GetSetting(settingKey); v != "" {
		return v
	}
	return def
}

// POST /api/admin/sb/tls/acme {name, server_id, server_name, method, cf_token, email}
// — issue a real Let's Encrypt certificate via acme.sh on the local host, install
// it to a stable path, and save a TLS profile that references those paths
// (tls.certificate_path / key_path). acme.sh's cron renews it in place; sing-box
// picks up the new file on its next reload. Local host only for now; remote
// servers must issue on-box or paste a cert.
func (a *API) handleAdminAcmeCert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		ServerID   int64  `json:"server_id"`
		ServerName string `json:"server_name"` // the domain to issue for
		Method     string `json:"method"`      // "http-01" | "webroot" | "dns-cf"
		CFToken    string `json:"cf_token"`
		Webroot    string `json:"webroot"`
		Email      string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	domain := strings.TrimSpace(req.ServerName)
	if req.Name == "" || domain == "" {
		fail(w, http.StatusBadRequest, "名称和域名必填")
		return
	}
	if req.ServerID != 0 {
		fail(w, http.StatusBadRequest, "在线申请当前仅支持本机；远程服务器请在该机器上用 acme.sh 申请后粘贴证书，或用自签")
		return
	}
	method := acmesh.Method(strings.TrimSpace(req.Method))
	if method == "" {
		method = acmesh.MethodHTTP01
	}

	unit := a.sbSetting("QZ_SINGBOX_UNIT", "sb_systemd_unit", "sing-box")
	cfgPath := a.sbSetting("QZ_SINGBOX_CONFIG", "sb_config_path", "/etc/sing-box/config.json")
	certDir := path.Join(path.Dir(cfgPath), "certs")

	// acme.sh can block on DNS propagation (dns_cf sleeps ~2min); allow headroom.
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	res, err := acmesh.Issue(ctx, acmesh.LocalRunner{}, acmesh.IssueOpts{
		Domain:    domain,
		Method:    method,
		CFToken:   strings.TrimSpace(req.CFToken),
		Webroot:   strings.TrimSpace(req.Webroot),
		Email:     strings.TrimSpace(req.Email),
		CertDir:   certDir,
		ReloadCmd: "systemctl restart " + unit,
	})
	if err != nil {
		fail(w, http.StatusBadGateway, "证书申请失败："+err.Error())
		return
	}

	server := map[string]interface{}{
		"enabled":          true,
		"server_name":      domain,
		"certificate_path": res.CertPath,
		"key_path":         res.KeyPath,
	}
	client := map[string]interface{}{
		"utls": map[string]interface{}{"enabled": true, "fingerprint": "chrome"},
	}
	sj, _ := json.Marshal(server)
	cj, _ := json.Marshal(client)
	id, err := a.st.SaveSbTls(&store.SbTls{ServerID: req.ServerID, Name: req.Name, Mode: "tls", ServerJSON: string(sj), ClientJSON: string(cj)})
	if err != nil {
		fail(w, http.StatusInternalServerError, "证书已申请，但保存配置失败")
		return
	}
	_ = a.sbctl.RebuildServer(req.ServerID)
	saved, _ := a.st.GetSbTls(id)
	ok(w, saved)
}

// validateCertKeyPair reports whether the PEM certificate and key are both
// parseable and form a matching pair (the key belongs to the cert). Returns a
// user-facing Chinese error describing the first problem found, or nil when ok.
func validateCertKeyPair(certPEM, keyPEM string) error {
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return fmt.Errorf("证书与私钥无效或不匹配：%v", err)
	}
	return nil
}

// GET /api/admin/sb/sni-test?host=www.microsoft.com[:port]&port=8443
// Tests TCP handshake latency to an SNI host (default port 443, or ?port=). Takes
// 3 samples and reports min/avg/max + resolve info. Used by the TLS editor to
// preview how well a candidate SNI domain performs before saving, and by the
// Reality handshake target test (which may use a non-443 port).
// normalizeSniAddr turns a user-supplied host (and optional explicit port) into
// a dialable host:port, or reports that the host is malformed.
//
// "是否已经带端口了" 曾用 strings.Contains(host, ":") 判断，裸 IPv6 字面量天生
// 含冒号，于是既不补端口、后面的 SplitHostPort 又解析不了，任何 IPv6 host 都
// 直接被判 "host 格式错误"。改用 SplitHostPort 试解析：成功即说明已带端口。
//
// 但只做这一步会让校验变宽松：JoinHostPort 会给任何含冒号的 host 加方括号，
// 于是 "a:b:c" 这类垃圾会被包成 "[a:b:c]:443" 一路通过，最终以 "无法连接"
// 而不是 "格式错误" 报错，掩盖了真正的原因。所以含冒号的 host 必须真的能被
// ParseIP 解析成 IP 才放行。
func normalizeSniAddr(host, port string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	// 已经是 host:port（含 [v6]:port）就原样用，显式 port 参数让位于内联端口。
	// 注意 SplitHostPort(":443") 是成功的（host 为空），必须单独挡掉。
	if h, _, err := net.SplitHostPort(host); err == nil {
		if h == "" {
			return "", fmt.Errorf("empty host in %q", host)
		}
		return host, nil
	}
	if port == "" {
		port = "443"
	}
	h := host
	if len(h) > 1 && h[0] == '[' && h[len(h)-1] == ']' {
		h = h[1 : len(h)-1]
	}
	if h == "" {
		return "", fmt.Errorf("empty host")
	}
	// 到这里 h 应当是域名或 IP 字面量。域名和 IPv4 都不含冒号；含冒号就只能是
	// IPv6，必须解析得出来——否则是 "a:b:c" / "[2001:db8::1"（括号未闭合）之类。
	if strings.Contains(h, ":") && net.ParseIP(h) == nil {
		return "", fmt.Errorf("malformed host %q", host)
	}
	return net.JoinHostPort(h, port), nil
}

func (a *API) handleAdminSniTest(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	if host == "" {
		fail(w, http.StatusBadRequest, "缺少 host 参数")
		return
	}
	addr, err := normalizeSniAddr(host, strings.TrimSpace(r.URL.Query().Get("port")))
	if err != nil {
		fail(w, http.StatusBadRequest, "host 格式错误")
		return
	}
	// 回显给前端的是不带端口的主机名；normalizeSniAddr 已保证 addr 可拆分。
	h, _, _ := net.SplitHostPort(addr)

	type sample struct {
		MS    float64 `json:"ms"`
		Error string  `json:"error,omitempty"`
	}
	const probes = 3
	samples := make([]sample, 0, probes)
	var sum, okCount float64
	var min, max float64
	for i := 0; i < probes; i++ {
		t0 := time.Now()
		conn, derr := net.DialTimeout("tcp", addr, 3*time.Second)
		dt := time.Since(t0).Seconds() * 1000
		if derr != nil {
			samples = append(samples, sample{MS: dt, Error: derr.Error()})
			continue
		}
		_ = conn.Close()
		samples = append(samples, sample{MS: dt})
		sum += dt
		okCount++
		if min == 0 || dt < min {
			min = dt
		}
		if dt > max {
			max = dt
		}
		time.Sleep(80 * time.Millisecond)
	}

	avg := 0.0
	status := "ok"
	if okCount > 0 {
		avg = sum / okCount
	} else {
		status = "unreachable"
	}
	if okCount > 0 && okCount < float64(probes) {
		status = "partial" // some probes failed
	}
	ok(w, J{
		"host":    h,
		"addr":    addr,
		"status":  status,
		"ok":      int(okCount),
		"total":   probes,
		"min_ms":  min,
		"avg_ms":  avg,
		"max_ms":  max,
		"samples": samples,
	})
}

// ---- sb_tls ----

// certInfoFromPEM 解析 PEM 证书，返回有效期信息（用于列表展示过期提醒）。
func certInfoFromPEM(pemStr string) map[string]interface{} {
	if pemStr == "" {
		return nil
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return map[string]interface{}{"error": "PEM 解析失败"}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return map[string]interface{}{"error": "证书解析失败: " + err.Error()}
	}
	now := time.Now()
	daysLeft := int(cert.NotAfter.Sub(now).Hours() / 24)
	return map[string]interface{}{
		"subject":     cert.Subject.CommonName,
		"issuer":      cert.Issuer.CommonName,
		"not_before":  cert.NotBefore.Unix(),
		"not_after":   cert.NotAfter.Unix(),
		"days_left":   daysLeft,
		"expired":     now.After(cert.NotAfter),
		"expiring":    daysLeft >= 0 && daysLeft <= 14,
	}
}

func (a *API) handleAdminListSbTls(w http.ResponseWriter, r *http.Request) {
	list, err := a.st.ListSbTls()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取失败")
		return
	}
	// 为每个证书 TLS 模式的配置附带证书有效期信息
	out := make([]map[string]interface{}, 0, len(list))
	for _, t := range list {
		m := map[string]interface{}{
			"id":          t.ID,
			"server_id":   t.ServerID,
			"name":        t.Name,
			"mode":        t.Mode,
			"server_json": t.ServerJSON,
			"client_json": t.ClientJSON,
			"created_at":  t.CreatedAt,
			"updated_at":  t.UpdatedAt,
		}
		if t.Mode == "tls" {
			var sj map[string]interface{}
			if json.Unmarshal([]byte(t.ServerJSON), &sj) == nil {
				if cert, ok := sj["certificate"].(string); ok && cert != "" {
					m["cert_info"] = certInfoFromPEM(cert)
				}
			}
		}
		out = append(out, m)
	}
	ok(w, out)
}

func (a *API) handleAdminSaveSbTls(w http.ResponseWriter, r *http.Request) {
	var t store.SbTls
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if strings.TrimSpace(t.Name) == "" {
		fail(w, http.StatusBadRequest, "名称必填")
		return
	}
	id := chi.URLParam(r, "id")
	if id != "" {
		t.ID = atoi(id)
	}
	newID, err := a.st.SaveSbTls(&t)
	if err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	if err := a.sbRebuild(); err != nil {
		fail(w, http.StatusBadGateway, "已保存，但应用到 sing-box 失败："+err.Error())
		return
	}
	saved, _ := a.st.GetSbTls(newID)
	ok(w, saved)
}

func (a *API) handleAdminDeleteSbTls(w http.ResponseWriter, r *http.Request) {
	if err := a.st.DeleteSbTls(atoi(chi.URLParam(r, "id"))); err != nil {
		if errors.Is(err, store.ErrInUse) {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	a.sbRebuildLog()
	ok(w, nil)
}

// POST /api/admin/sb/tls/reality — convenience: build a complete Reality TLS
// profile (server + client JSON) with a freshly generated keypair.
func (a *API) handleAdminCreateRealityTls(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string   `json:"name"`
		ServerID        int64    `json:"server_id"`
		ServerName      string   `json:"server_name"`      // SNI shown to clients, e.g. www.tesla.com
		HandshakeServer string   `json:"handshake_server"` // real TLS dest, defaults to ServerName
		HandshakePort   int      `json:"handshake_port"`   // defaults 443
		Fingerprint     string   `json:"fingerprint"`      // utls fp, defaults chrome
		// Pre-generated keys from frontend (optional; backend generates if empty)
		PrivateKey string   `json:"private_key"`
		PublicKey  string   `json:"public_key"`
		ShortID    string   `json:"short_id"`     // 单个（向后兼容）
		ShortIDs   []string `json:"short_ids"`    // 多个（优先）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.ServerName = strings.TrimSpace(req.ServerName)
	if req.Name == "" || req.ServerName == "" {
		fail(w, http.StatusBadRequest, "名称和 SNI 必填")
		return
	}
	if req.HandshakeServer == "" {
		req.HandshakeServer = req.ServerName
	}
	if req.HandshakePort == 0 {
		req.HandshakePort = 443
	}

	priv, pub := req.PrivateKey, req.PublicKey
	// Generate keys only if not provided by frontend
	if priv == "" || pub == "" {
		var err error
		priv, pub, err = singbox.GenerateRealityKeypair()
		if err != nil {
			fail(w, http.StatusInternalServerError, "生成密钥失败")
			return
		}
	}
	// 构建 short_id 列表：优先用 short_ids 数组，否则用单个 short_id
	sids := make([]string, 0, len(req.ShortIDs))
	for _, s := range req.ShortIDs {
		if s = strings.TrimSpace(s); s != "" {
			sids = append(sids, s)
		}
	}
	if len(sids) == 0 {
		sid := strings.TrimSpace(req.ShortID)
		if sid == "" {
			sid, _ = singbox.GenerateShortID(8)
		}
		sids = []string{sid}
	}

	server := map[string]interface{}{
		"enabled":     true,
		"server_name": req.ServerName,
		"reality": map[string]interface{}{
			"enabled":     true,
			"handshake":   map[string]interface{}{"server": req.HandshakeServer, "server_port": req.HandshakePort},
			"private_key": priv,
			"short_id":    sids,
		},
	}
	client := map[string]interface{}{
		"server_name": req.ServerName,
		"public_key":  pub,
		"short_id":    sids[0],
		"reality":     map[string]interface{}{"public_key": pub},
		"utls":        map[string]interface{}{"enabled": true, "fingerprint": fpOrDefault(req.Fingerprint)},
	}
	sj, _ := json.Marshal(server)
	cj, _ := json.Marshal(client)
	id, err := a.st.SaveSbTls(&store.SbTls{ServerID: req.ServerID, Name: req.Name, Mode: "reality", ServerJSON: string(sj), ClientJSON: string(cj)})
	if err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	saved, _ := a.st.GetSbTls(id)
	ok(w, saved)
}

var validFingerprints = map[string]bool{
	"chrome": true, "firefox": true, "safari": true, "ios": true, "android": true,
	"edge": true, "360": true, "qq": true, "random": true, "randomized": true,
}

func fpOrDefault(fp string) string {
	if validFingerprints[fp] {
		return fp
	}
	return "chrome"
}

// tlsVersion normalizes a TLS version string to sing-box's form ("1.2"/"1.3").
func tlsVersion(v string) string {
	switch v {
	case "1.2", "1.3":
		return v
	}
	return ""
}

// PUT /api/admin/sb/tls/reality/{id} — edit a Reality profile's name/SNI/
// handshake/short_ids from structured fields, preserving the existing keypair.
func (a *API) handleAdminUpdateRealityTls(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	cur, _ := a.st.GetSbTls(id)
	if cur == nil {
		fail(w, http.StatusNotFound, "配置不存在")
		return
	}
	var req struct {
		Name            string   `json:"name"`
		ServerID        int64    `json:"server_id"`
		ServerName      string   `json:"server_name"`
		HandshakeServer string   `json:"handshake_server"`
		HandshakePort   int      `json:"handshake_port"`
		Fingerprint     string   `json:"fingerprint"`
		ShortIDs        []string `json:"short_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.ServerName = strings.TrimSpace(req.ServerName)
	if req.Name == "" || req.ServerName == "" {
		fail(w, http.StatusBadRequest, "名称和 SNI 必填")
		return
	}
	if req.HandshakeServer == "" {
		req.HandshakeServer = req.ServerName
	}
	if req.HandshakePort == 0 {
		req.HandshakePort = 443
	}
	var server, client map[string]interface{}
	_ = json.Unmarshal([]byte(cur.ServerJSON), &server)
	_ = json.Unmarshal([]byte(cur.ClientJSON), &client)
	if server == nil {
		server = map[string]interface{}{"enabled": true}
	}
	server["server_name"] = req.ServerName
	reality, _ := server["reality"].(map[string]interface{})
	if reality == nil {
		reality = map[string]interface{}{"enabled": true}
		server["reality"] = reality
	}
	reality["handshake"] = map[string]interface{}{"server": req.HandshakeServer, "server_port": req.HandshakePort}
	// 更新 short_id 列表（如果前端提供了）
	if len(req.ShortIDs) > 0 {
		sids := make([]string, 0, len(req.ShortIDs))
		for _, s := range req.ShortIDs {
			if s = strings.TrimSpace(s); s != "" {
				sids = append(sids, s)
			}
		}
		if len(sids) > 0 {
			reality["short_id"] = sids
			if client == nil {
				client = map[string]interface{}{}
			}
			client["short_id"] = sids[0]
		}
	}
	if client == nil {
		client = map[string]interface{}{}
	}
	client["server_name"] = req.ServerName
	client["utls"] = map[string]interface{}{"enabled": true, "fingerprint": fpOrDefault(req.Fingerprint)}
	sj, _ := json.Marshal(server)
	cj, _ := json.Marshal(client)
	if _, err := a.st.SaveSbTls(&store.SbTls{ID: id, ServerID: req.ServerID, Name: req.Name, Mode: "reality", ServerJSON: string(sj), ClientJSON: string(cj)}); err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	a.sbRebuildLog()
	saved, _ := a.st.GetSbTls(id)
	ok(w, saved)
}

// POST /api/admin/sb/tls/cert  and  PUT /api/admin/sb/tls/cert/{id} — create or
// edit a regular (non-Reality) TLS profile from structured fields. On edit, a
// blank certificate/key keeps the existing one (so the cert needn't be re-pasted).
func (a *API) handleAdminSaveCertTls(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string   `json:"name"`
		ServerID    int64    `json:"server_id"`
		ServerName  string   `json:"server_name"`
		Certificate string   `json:"certificate"`
		Key         string   `json:"key"`
		Insecure    bool     `json:"insecure"`
		ALPN        []string `json:"alpn"`
		Fingerprint string   `json:"fingerprint"`
		MinVersion  string   `json:"min_version"`
		MaxVersion  string   `json:"max_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.ServerName = strings.TrimSpace(req.ServerName)
	if req.Name == "" || req.ServerName == "" {
		fail(w, http.StatusBadRequest, "名称和 SNI 必填")
		return
	}
	var id int64
	if p := chi.URLParam(r, "id"); p != "" {
		id = atoi(p)
	}
	// On edit, keep existing cert/key when the form leaves them blank.
	if id != 0 && (req.Certificate == "" || req.Key == "") {
		if cur, _ := a.st.GetSbTls(id); cur != nil {
			var s map[string]interface{}
			_ = json.Unmarshal([]byte(cur.ServerJSON), &s)
			if req.Certificate == "" {
				req.Certificate, _ = s["certificate"].(string)
			}
			if req.Key == "" {
				req.Key, _ = s["key"].(string)
			}
		}
	}
	if req.Certificate == "" || req.Key == "" {
		fail(w, http.StatusBadRequest, "证书和私钥必填")
		return
	}
	// Pre-validate the PEM pair here so a bad paste is caught at save time with a
	// clear message, instead of surfacing later as an opaque sing-box apply error.
	if err := validateCertKeyPair(req.Certificate, req.Key); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	server := map[string]interface{}{
		"enabled":     true,
		"server_name": req.ServerName,
		"certificate": req.Certificate,
		"key":         req.Key,
	}
	if len(req.ALPN) > 0 {
		server["alpn"] = req.ALPN
	}
	if v := tlsVersion(req.MinVersion); v != "" {
		server["min_version"] = v
	}
	if v := tlsVersion(req.MaxVersion); v != "" {
		server["max_version"] = v
	}
	client := map[string]interface{}{
		"insecure": req.Insecure,
		"utls":     map[string]interface{}{"enabled": true, "fingerprint": fpOrDefault(req.Fingerprint)},
	}
	sj, _ := json.Marshal(server)
	cj, _ := json.Marshal(client)
	newID, err := a.st.SaveSbTls(&store.SbTls{ID: id, ServerID: req.ServerID, Name: req.Name, Mode: "tls", ServerJSON: string(sj), ClientJSON: string(cj)})
	if err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	a.sbRebuildLog()
	saved, _ := a.st.GetSbTls(newID)
	ok(w, saved)
}

// ---- sb_inbounds ----

func (a *API) handleAdminListSbInbounds(w http.ResponseWriter, r *http.Request) {
	list, err := a.st.ListSbInbounds()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取失败")
		return
	}
	// 为每个入站附带当前用户数（按 tag 统计有权限的用户）
	usersByTag, _ := a.st.BuildUsersByTag(time.Now().Unix())
	out := make([]map[string]interface{}, 0, len(list))
	for _, n := range list {
		m := map[string]interface{}{
			"id":          n.ID,
			"server_id":   n.ServerID,
			"type":        n.Type,
			"tag":         n.Tag,
			"listen":      n.Listen,
			"listen_port": n.ListenPort,
			"tls_id":              n.TlsID,
			"options":             n.Options,
			"enabled":             n.Enabled,
			"sort_order":          n.SortOrder,
			"upstream_inbound_id": n.UpstreamInboundID,
			"created_at":          n.CreatedAt,
			"updated_at":          n.UpdatedAt,
			"user_count":          len(usersByTag[n.Tag]),
		}
		out = append(out, m)
	}
	ok(w, out)
}

var sbInboundTypes = map[string]bool{"vless": true, "hysteria2": true, "tuic": true, "trojan": true, "vmess": true, "shadowsocks": true, "anytls": true, "hysteria": true, "mixed": true}

func (a *API) handleAdminSaveSbInbound(w http.ResponseWriter, r *http.Request) {
	var n store.SbInbound
	// On update, decode onto the stored row. SaveSbInbound writes every column,
	// including relay_secret — which is json:"-" and therefore CANNOT come from
	// the client. Decoding into a zero value blanked it on every save, including
	// a plain enable/disable toggle: the landing inbound then lazily minted a new
	// secret while the relay inbound's server kept dialling with the old one, so
	// the chain stayed down until some unrelated change rebuilt that server.
	// sort_order was lost the same way.
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		cur, err := a.st.GetSbInbound(int64(atoi(idStr)))
		if err != nil || cur == nil {
			fail(w, http.StatusNotFound, "入站不存在")
			return
		}
		n = *cur
	}
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	n.Type = strings.TrimSpace(n.Type)
	n.Tag = strings.TrimSpace(n.Tag)
	if !sbInboundTypes[n.Type] {
		fail(w, http.StatusBadRequest, "不支持的协议类型")
		return
	}
	if n.Tag == "" || n.ListenPort <= 0 || n.ListenPort > 65535 {
		fail(w, http.StatusBadRequest, "tag 必填、端口需在 1-65535")
		return
	}
	// Tags are internal identifiers (sing-box config keys, relay tag suffixes,
	// the node→group join key); keep them whitespace-free so they stay greppable
	// in a config dump and unambiguous in logs. The displayed node name is a
	// separate field on the 节点 page and is free to contain spaces.
	if strings.ContainsAny(n.Tag, " \t\r\n") {
		fail(w, http.StatusBadRequest, "tag 不能包含空格")
		return
	}
	// A mixed (HTTP/SOCKS5) inbound is a plain proxy for tools like 1Panel/Docker,
	// not a circumvention protocol: it may carry a normal-cert TLS block (→ HTTPS
	// proxy) but never Reality, which HTTP/SOCKS clients cannot speak.
	if n.Type == "mixed" && n.TlsID != 0 {
		if tls, _ := a.st.GetSbTls(n.TlsID); tls != nil && strings.Contains(tls.ServerJSON, "\"reality\"") {
			fail(w, http.StatusBadRequest, "mixed 代理不支持 Reality，请选普通 TLS 证书或留空")
			return
		}
	}
	// 端口冲突检测：同服务器同端口不允许重复
	if id := chi.URLParam(r, "id"); id != "" {
		n.ID = atoi(id)
	}
	if conflict, existingTag, err := a.st.SbInboundPortConflict(&n); err != nil {
		fail(w, http.StatusInternalServerError, "端口冲突检测失败")
		return
	} else if conflict {
		fail(w, http.StatusBadRequest, fmt.Sprintf("端口 %d 已被入站「%s」占用", n.ListenPort, existingTag))
		return
	}
	if n.Options != "" {
		var opts map[string]interface{}
		if err := json.Unmarshal([]byte(n.Options), &opts); err != nil {
			fail(w, http.StatusBadRequest, "options 不是合法 JSON")
			return
		}
		// Shadowsocks-2022 needs a server PSK; generate one if absent.
		if n.Type == "shadowsocks" {
			method, _ := opts["method"].(string)
			if pw, _ := opts["password"].(string); pw == "" {
				key := make([]byte, singbox.SSKeyLen(method))
				if _, err := rand.Read(key); err != nil {
					fail(w, http.StatusInternalServerError, "生成密钥失败")
					return
				}
				opts["password"] = base64.StdEncoding.EncodeToString(key)
				if b, err := json.Marshal(opts); err == nil {
					n.Options = string(b)
				}
			}
		}
	}
	// Relay validation: the upstream must be an existing, different inbound.
	if n.UpstreamInboundID != 0 {
		if n.UpstreamInboundID == n.ID {
			fail(w, http.StatusBadRequest, "落地入站不能是自己")
			return
		}
		up, err := a.st.GetSbInbound(n.UpstreamInboundID)
		if err != nil || up == nil {
			fail(w, http.StatusBadRequest, "所选落地入站不存在")
			return
		}
		if up.UpstreamInboundID != 0 {
			fail(w, http.StatusBadRequest, "落地入站本身是中转入站，不支持多级链式")
			return
		}
	}
	// Capture the pre-save upstream so a rebuild also reaches the OLD landing
	// server when the upstream is changed or cleared.
	var oldUpstream int64
	if n.ID != 0 {
		if prev, _ := a.st.GetSbInbound(n.ID); prev != nil {
			oldUpstream = prev.UpstreamInboundID
		}
	}
	newID, err := a.st.SaveSbInbound(&n)
	if err != nil {
		fail(w, http.StatusInternalServerError, "保存失败（tag 可能重复）")
		return
	}
	if err := a.sbctl.RebuildServer(n.ServerID); err != nil {
		fail(w, http.StatusBadGateway, "已保存，但应用到 sing-box 失败："+err.Error())
		return
	}
	// A relay inbound's landing lives on another server that must also rebuild to
	// inject (or drop) the relay credential in its users[].
	a.rebuildLandingServers(n.UpstreamInboundID, oldUpstream)
	saved, _ := a.st.GetSbInbound(newID)
	ok(w, saved)
}

// rebuildLandingServers rebuilds the server(s) hosting the given landing
// inbound(s) so their users[] pick up (or drop) the relay credential. Duplicate
// and zero ids are ignored, as is the case where the landing shares the server
// just rebuilt by the caller.
func (a *API) rebuildLandingServers(landingIDs ...int64) {
	seen := map[int64]bool{}
	for _, id := range landingIDs {
		if id == 0 {
			continue
		}
		up, _ := a.st.GetSbInbound(id)
		if up == nil || seen[up.ServerID] {
			continue
		}
		seen[up.ServerID] = true
		_ = a.sbctl.RebuildServer(up.ServerID)
	}
}

func (a *API) handleAdminDeleteSbInbound(w http.ResponseWriter, r *http.Request) {
	inboundID := int64(atoi(chi.URLParam(r, "id")))
	// 先查询归属服务器，删除后只重建该服务器，避免影响其他服务器。
	ib, _ := a.st.GetSbInbound(inboundID)
	if err := a.st.DeleteSbInbound(inboundID); err != nil {
		fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if ib != nil {
		_ = a.sbctl.RebuildServer(ib.ServerID)
		// If this was a relay inbound, its landing server must rebuild to drop the
		// now-unused relay credential.
		a.rebuildLandingServers(ib.UpstreamInboundID)
	} else {
		// 查不到归属服务器（异常情况）时全量重建兜底，避免被删的 inbound
		// 残留在某个运行中的配置里。
		a.sbRebuildLog()
	}
	ok(w, nil)
}

// buildPreviewConfig renders the sing-box config for a specific server
// (serverID <= 0 = local). Shared by the preview and correctness-check
// endpoints so both validate exactly what they display.
func (a *API) buildPreviewConfig(serverID int64) ([]byte, error) {
	byTag, err := a.st.BuildUsersByTag(nowUnix())
	if err != nil {
		return nil, fmt.Errorf("构建用户映射失败: %w", err)
	}
	base, _ := a.st.GetSetting("sb_base_config")
	if base == "" {
		base = singbox.DefaultBaseConfig
	}
	listen, _ := a.st.GetSetting("sb_v2ray_listen")
	if listen == "" {
		listen = "127.0.0.1:18080"
	}
	if serverID > 0 {
		return a.st.BuildSingboxConfigForServer(serverID, base, listen, byTag)
	}
	return a.st.BuildSingboxConfig(base, listen, byTag)
}

// GET /api/admin/sb/preview?server_id=N — render the sing-box config for a
// specific server (server_id=0 or omitted = local).
func (a *API) handleAdminSbPreview(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.buildPreviewConfig(atoi(r.URL.Query().Get("server_id")))
	if err != nil {
		fail(w, http.StatusInternalServerError, "生成配置失败")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(cfg)
}

// GET /api/admin/sb/check?server_id=N — generate the config for a server and
// validate it with `sing-box check`, the same gate Apply uses before shipping.
// Exposed read-only so the admin can catch a bad config before it goes live.
// Always returns 200 with {ok,stage,output,...}; ok=false means "config has a
// problem", not "the request failed".
func (a *API) handleAdminSbCheck(w http.ResponseWriter, r *http.Request) {
	serverID := atoi(r.URL.Query().Get("server_id"))
	cfg, err := a.buildPreviewConfig(serverID)
	if err != nil {
		ok(w, J{"ok": false, "stage": "generate", "output": "生成配置失败：" + err.Error()})
		return
	}

	// Structural sanity + a couple of panel-level lints that don't need the
	// binary (empty inbounds is a common "why is my node dead" cause).
	var doc struct {
		Inbounds  []struct {
			Tag        string `json:"tag"`
			ListenPort int    `json:"listen_port"`
		} `json:"inbounds"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal(cfg, &doc); err != nil {
		ok(w, J{"ok": false, "stage": "json", "output": "生成的配置不是合法 JSON：" + err.Error()})
		return
	}
	var warnings []string
	if len(doc.Inbounds) == 0 {
		warnings = append(warnings, "该机器没有任何入站，inbounds 为空（配置能通过校验但不会提供任何服务）。")
	}
	seen := map[int]string{}
	for _, ib := range doc.Inbounds {
		if ib.ListenPort == 0 {
			continue
		}
		if prev, dup := seen[ib.ListenPort]; dup {
			warnings = append(warnings, fmt.Sprintf("端口 %d 被多个入站占用（%s、%s），启动时会冲突。", ib.ListenPort, prev, ib.Tag))
		}
		seen[ib.ListenPort] = ib.Tag
	}

	base := J{"inbounds": len(doc.Inbounds), "outbounds": len(doc.Outbounds), "warnings": warnings}

	bin := sbproc.FindSingBoxBin()
	if bin == "" {
		base["ok"] = len(warnings) == 0
		base["stage"] = "no-binary"
		base["output"] = "未找到 sing-box 可执行文件，仅校验了 JSON 结构（未做 sing-box check）。可设置 QZ_SINGBOX_BIN 或把 sing-box 装到 PATH 后重试。"
		ok(w, base)
		return
	}

	tmp, err := os.CreateTemp("", "qz-precheck-*.json")
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建临时文件失败")
		return
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.Write(cfg); err != nil {
		tmp.Close()
		fail(w, http.StatusInternalServerError, "写入临时文件失败")
		return
	}
	tmp.Close()

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, cerr := exec.CommandContext(ctx, bin, "check", "-c", path).CombinedOutput()
	base["stage"] = "check"
	if cerr != nil {
		base["ok"] = false
		base["output"] = strings.TrimSpace(string(out)+"\n"+cerr.Error())
		ok(w, base)
		return
	}
	base["ok"] = len(warnings) == 0
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = "sing-box check 通过，配置合法。"
	}
	base["output"] = msg
	ok(w, base)
}

func nowUnix() int64 { return time.Now().Unix() }

// GET /api/admin/sb/port-check?server_id=N&port=443
// 测试入站端口是否对外开放（TCP 握手）。server_id=0 表示本机（测 127.0.0.1），
// 远程服务器测其 host:port。
func (a *API) handleAdminPortCheck(w http.ResponseWriter, r *http.Request) {
	port := atoi(r.URL.Query().Get("port"))
	if port <= 0 || port > 65535 {
		fail(w, http.StatusBadRequest, "端口需在 1-65535")
		return
	}
	serverID := atoi(r.URL.Query().Get("server_id"))
	host := "127.0.0.1"
	if serverID > 0 {
		sv, err := a.st.GetServer(serverID)
		if err != nil || sv == nil {
			fail(w, http.StatusNotFound, "服务器不存在")
			return
		}
		host = sv.Host
		if host == "" {
			fail(w, http.StatusBadRequest, "该服务器未配置主机地址")
			return
		}
	}
	// net.JoinHostPort brackets IPv6 literals correctly ("[::1]:443"); a plain
	// "%s:%d" would produce an unparseable address for an IPv6 host.
	addr := net.JoinHostPort(host, itoa(port))
	t0 := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	ms := time.Since(t0).Seconds() * 1000
	if err != nil {
		ok(w, J{"reachable": false, "addr": addr, "ms": ms, "error": err.Error()})
		return
	}
	_ = conn.Close()
	ok(w, J{"reachable": true, "addr": addr, "ms": ms})
}

// GET /api/admin/sb/import-remote/list-files?server_id=N — SSH into the remote
// server and list .json config files in common sing-box directories.
func (a *API) handleAdminImportRemoteListFiles(w http.ResponseWriter, r *http.Request) {
	serverID := atoi(r.URL.Query().Get("server_id"))
	if serverID <= 0 {
		fail(w, http.StatusBadRequest, "server_id 必填")
		return
	}
	sv, err := a.st.GetServer(serverID)
	if err != nil || sv == nil {
		fail(w, http.StatusNotFound, "服务器不存在")
		return
	}

	rm := a.newRemoteManager(15 * time.Second)
	cfg := &sshctl.ServerConfig{
		ID: sv.ID, Host: sv.Host, Port: sv.Port,
		SSHUser: sv.SSHUser, SSHKey: sv.SSHKey, SSHKeyPass: sv.SSHKeyPass, SSHPassword: sv.SSHPassword,
		ConfigPath: sv.ConfigPath, SystemdUnit: sv.SystemdUnit, SingBoxBin: sv.SingBoxBin, HostKey: sv.HostKey,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	files, err := rm.ListRemoteConfigFiles(ctx, cfg)
	if err != nil {
		fail(w, http.StatusBadGateway, "扫描远程文件失败: "+err.Error())
		return
	}

	ok(w, J{"files": files})
}

// GET /api/admin/sb/import-remote/preview?server_id=N&config_path=xxx — SSH
// into the remote server, read the specified config file, and return the
// inbounds array for the admin to review before importing.
func (a *API) handleAdminImportRemotePreview(w http.ResponseWriter, r *http.Request) {
	serverID := atoi(r.URL.Query().Get("server_id"))
	configPath := strings.TrimSpace(r.URL.Query().Get("config_path"))
	if serverID <= 0 {
		fail(w, http.StatusBadRequest, "server_id 必填")
		return
	}
	if configPath == "" {
		fail(w, http.StatusBadRequest, "config_path 必填")
		return
	}
	sv, err := a.st.GetServer(serverID)
	if err != nil || sv == nil {
		fail(w, http.StatusNotFound, "服务器不存在")
		return
	}

	rm := a.newRemoteManager(15 * time.Second)
	cfg := &sshctl.ServerConfig{
		ID: sv.ID, Host: sv.Host, Port: sv.Port,
		SSHUser: sv.SSHUser, SSHKey: sv.SSHKey, SSHKeyPass: sv.SSHKeyPass, SSHPassword: sv.SSHPassword,
		ConfigPath: sv.ConfigPath, SystemdUnit: sv.SystemdUnit, SingBoxBin: sv.SingBoxBin, HostKey: sv.HostKey,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	raw, err := rm.ReadRemoteConfigAtPath(ctx, cfg, configPath)
	if err != nil {
		fail(w, http.StatusBadGateway, "读取远程配置失败: "+err.Error())
		return
	}

	// Parse the inbounds from the raw config
	var full struct {
		Inbounds []json.RawMessage `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &full); err != nil {
		fail(w, http.StatusBadGateway, "远程配置 JSON 解析失败: "+err.Error())
		return
	}
	if len(full.Inbounds) == 0 {
		fail(w, http.StatusBadGateway, "远程配置中未找到入站协议")
		return
	}

	// Extract type, tag, listen_port from each inbound for display
	type inboundInfo struct {
		Type        string          `json:"type"`
		Tag         string          `json:"tag"`
		ListenPort  int             `json:"listen_port"`
		Raw         json.RawMessage `json:"raw"`
	}
	var inbounds []inboundInfo
	for _, ib := range full.Inbounds {
		var meta struct {
			Type       string `json:"type"`
			Tag        string `json:"tag"`
			Listen     string `json:"listen"`
			ListenPort int    `json:"listen_port"`
		}
		_ = json.Unmarshal(ib, &meta)
		inbounds = append(inbounds, inboundInfo{
			Type: meta.Type, Tag: meta.Tag, ListenPort: meta.ListenPort, Raw: ib,
		})
	}

	ok(w, J{
		"server_id":   serverID,
		"server_name": sv.Name,
		"config_path": configPath,
		"inbounds":    inbounds,
	})
}
