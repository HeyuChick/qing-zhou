package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qingzhou/internal/store"
	"qingzhou/internal/subconv"
)

func TestSubscriptionProfileLinksAreOptIn(t *testing.T) {
	base := "https://panel.example/sub/TOKEN"
	cn := subscriptionProfileLinks(base, subconv.ProfileCNDirect)
	if cn["url"] != base+"?profile=cn-direct" {
		t.Errorf("cn-direct url = %v", cn["url"])
	}
	formats, _ := cn["formats"].(J)
	if formats["clash"] != base+"?profile=cn-direct&format=clash" {
		t.Errorf("clash profile URL = %v", formats["clash"])
	}
	if formats["base64"] != base+"?format=base64" {
		t.Errorf("base64 falsely carries a routing profile: %v", formats["base64"])
	}
}

// fork 定制语义：不带 profile 参数 = cn-direct（机场用户期望国内直连，存量
// 已导入链接免重导）；带参数但拼写错误 = 上游 legacy 行为（模板原样，不揣测
// 用户意图）；显式 proxy-all = 全代理。
func TestPublicSubscriptionProfileDoesNotChangeLegacy(t *testing.T) {
	a, st := newResetSubAPI(t)
	_, err := st.CreateUser(store.NewUser{Username: "profile-user", PasswordHash: "x", SubToken: "PROFILE_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}

	get := func(query string) string {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/sub/PROFILE_TOKEN"+query, nil)
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", query, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	def := get("?format=clash")
	unknown := get("?format=clash&profile=typo")
	cn := get("?format=clash&profile=cn-direct")
	all := get("?format=clash&profile=proxy-all")
	if def != cn {
		t.Error("无参数订阅应与显式 cn-direct 渲染一致（fork 默认）")
	}
	if !strings.Contains(def, "GEOSITE,CN,DIRECT") {
		t.Error("无参数 Clash 订阅缺少中国直连规则（fork 默认 cn-direct）")
	}
	if strings.Contains(unknown, "GEOSITE,CN,") {
		t.Error("拼写错误的 profile 不应被当成 cn-direct 或 proxy-all（维持上游 legacy 行为）")
	}
	if !strings.Contains(all, "GEOSITE,CN,✈️ 节点选择") {
		t.Error("proxy-all Clash routing is missing")
	}

	// sing-box 侧同理：无参数 → geosite-cn/geoip-cn 直连；显式 proxy-all → 无中国分流
	sbDef := get("?format=singbox")
	if !strings.Contains(sbDef, `"tag": "geosite-cn"`) && !strings.Contains(sbDef, `"tag":"geosite-cn"`) {
		t.Error("无参数 sing-box 订阅缺少 geosite-cn 规则集（fork 默认 cn-direct）")
	}
	sbAll := get("?format=singbox&profile=proxy-all")
	if strings.Contains(sbAll, "geosite-cn") {
		t.Error("proxy-all sing-box 订阅不应包含中国分流规则")
	}
}

func TestSurgeManagedConfigRetainsExplicitProfile(t *testing.T) {
	a, st := newResetSubAPI(t)
	_, err := st.CreateUser(store.NewUser{Username: "surge-profile", PasswordHash: "x", SubToken: "SURGE_PROFILE"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/sub/SURGE_PROFILE?format=surge&profile=proxy-all", nil)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/sub/SURGE_PROFILE?profile=proxy-all interval=") {
		t.Errorf("managed config lost profile: %s", w.Body.String())
	}
}
