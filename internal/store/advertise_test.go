package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/singbox"
)

// 品牌定制守护：入站 options 里的 advertise_host / advertise_port 只影响分享
// 链接对外宣告的连接地址，不得进入 sing-box 入站配置本体（sing-box 拒绝未知
// 字段），也不得改变入站实际监听端口。这是 CDN-WS 中转入站（本机 127.0.0.1
// 监听、客户端经 Cloudflare/nginx 反代进入）能按用户计量的前提。
func TestAdvertiseOverrideShapesLinkOnly(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "cdnuser")
	pkg := mkPlan(t, st, "CDN套餐", 10, 100, 30)
	buy(t, st, uid, pkg)

	_, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "cdn-ws", Listen: "127.0.0.1", ListenPort: 10002,
		Options: `{"transport":{"type":"ws","path":"/relay"},"advertise_host":"cdn.example.com","advertise_port":443}`,
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	gid, err := st.CreateGroup(NodeGroup{Name: "CDN组"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPlanGroups(pkg.ID, []int64{gid}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateNode(Node{Type: "self_built", Name: "CDN中转", InboundTag: "cdn-ws", Enabled: true, GroupIDs: []int64{gid}}); err != nil {
		t.Fatal(err)
	}

	u, _ := st.UserByID(uid)
	links := st.BuildSelfBuiltLinks(u, "203.0.113.9")
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1", len(links))
	}
	l := links[0].Link
	if !strings.Contains(l, "@cdn.example.com:443?") {
		t.Errorf("link does not advertise the CDN host/port: %s", l)
	}
	if !strings.Contains(l, "type=ws") || !strings.Contains(l, "path=%2Frelay") {
		t.Errorf("link lost the ws transport: %s", l)
	}
	if strings.Contains(l, "203.0.113.9") {
		t.Errorf("link still advertises the raw origin host: %s", l)
	}

	// 配置本体：advertise 键必须被剔除，监听端口保持本机端口。
	users, err := st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(cfg, &doc); err != nil {
		t.Fatal(err)
	}
	var inbound map[string]any
	for _, ib := range doc["inbounds"].([]any) {
		m := ib.(map[string]any)
		if m["tag"] == "cdn-ws" {
			inbound = m
		}
	}
	if inbound == nil {
		t.Fatal("cdn-ws inbound missing from generated config")
	}
	if _, ok := inbound["advertise_host"]; ok {
		t.Error("advertise_host leaked into the sing-box inbound config")
	}
	if _, ok := inbound["hop_ports"]; ok {
		t.Error("hop_ports leaked into the sing-box inbound config")
	}
	if inbound["listen_port"].(float64) != 10002 {
		t.Errorf("listen_port = %v, want the real listener port 10002", inbound["listen_port"])
	}
	tr, _ := inbound["transport"].(map[string]any)
	if tr == nil || tr["type"] != "ws" {
		t.Errorf("ws transport missing from generated config: %v", inbound["transport"])
	}
}


// 品牌定制守护：hop_ports 渲染为 hysteria2 链接的 mport 参数（v2rayN 等共享链接
// 客户端）、sing-box 订阅的 server_ports + hop_interval（SFA）、mihomo 的 ports
// （Clash 系），且不进入节点配置本体。这是「hy2-443端口跳跃」节点按用户计量的前提。
func TestHopPortsRendersIntoLinks(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "hopuser")
	pkg := mkPlan(t, st, "跳跃套餐", 10, 100, 30)
	buy(t, st, uid, pkg)

	_, err := st.SaveSbInbound(&SbInbound{
		Type: "hysteria2", Tag: "hy2-443", Listen: "::", ListenPort: 443,
		Options: `{"hop_ports":"20000-50000"}`,
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	gid, err := st.CreateGroup(NodeGroup{Name: "跳跃组"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPlanGroups(pkg.ID, []int64{gid}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateNode(Node{Type: "self_built", Name: "hy2-443端口跳跃", InboundTag: "hy2-443", Enabled: true, GroupIDs: []int64{gid}}); err != nil {
		t.Fatal(err)
	}

	u, _ := st.UserByID(uid)
	links := st.BuildSelfBuiltLinks(u, "203.0.113.9")
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1", len(links))
	}
	l := links[0].Link
	if !strings.Contains(l, "@203.0.113.9:443?") {
		t.Errorf("link should keep the real listener port: %s", l)
	}
	if !strings.Contains(l, "mport=20000-50000") {
		t.Errorf("link missing mport: %s", l)
	}

	users, err := st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "", users)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "hop_ports") {
		t.Error("hop_ports leaked into the generated sing-box config")
	}

}


// 品牌定制守护：自签证书入站（有 pin）必须同时下发 insecure=1——pin 只是支持
// pinSHA256 的客户端的加固项，sing-box 订阅渲染不还原 pin，没有 insecure 的
// 自签节点在 SFA 等客户端直接 x509 校验失败。
func TestSelfSignedPinAlsoAdvertisesInsecure(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "pinuser")
	pkg := mkPlan(t, st, "自签套餐", 10, 100, 30)
	buy(t, st, uid, pkg)

	selfSigned := mustSelfSignedCert(t)
	tlsID, err := st.SaveSbTls(&SbTls{
		Name: "自签", Mode: "tls",
		ServerJSON: `{"enabled":true,"server_name":"pin.example.com","certificate":` + jsonQuote(selfSigned) + `}`,
		ClientJSON: `{"server_name":"pin.example.com"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.SaveSbInbound(&SbInbound{
		Type: "hysteria2", Tag: "hy2-pin", Listen: "::", ListenPort: 4567,
		TlsID: tlsID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := st.CreateGroup(NodeGroup{Name: "自签组"})
	_ = st.SetPlanGroups(pkg.ID, []int64{gid})
	if _, err := st.CreateNode(Node{Type: "self_built", Name: "hy2-pin节点", InboundTag: "hy2-pin", Enabled: true, GroupIDs: []int64{gid}}); err != nil {
		t.Fatal(err)
	}

	u, _ := st.UserByID(uid)
	links := st.BuildSelfBuiltLinks(u, "203.0.113.9")
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1", len(links))
	}
	l := links[0].Link
	if !strings.Contains(l, "insecure=1") {
		t.Errorf("self-signed pin link must also carry insecure=1: %s", l)
	}
	if !strings.Contains(l, "pinSHA256=") {
		t.Errorf("self-signed link should carry pinSHA256: %s", l)
	}
}

func jsonQuote(pem string) string {
	b, _ := json.Marshal(pem)
	return string(b)
}

func mustSelfSignedCert(t *testing.T) string {
	t.Helper()
	// 复用 singbox 包的自签生成器，保证与线上证书形态一致。
	pem, _, err := singbox.GenerateSelfSignedCert("pin.example.com", 365)
	if err != nil {
		t.Skipf("no self-signed generator: %v", err)
	}
	return pem
}
