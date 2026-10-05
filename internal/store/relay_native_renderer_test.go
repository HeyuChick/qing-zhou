package store

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"qingzhou/internal/singbox"
)

// A nil Store is intentional: explicit identities and complete caches must
// render from the snapshot alone, without either reads or lazy secret writes.
func renderNativeRelayFixture(t *testing.T, protocol string, opts map[string]interface{}, tls *SbTls) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	landing := &SbInbound{ID: 7, ServerID: 2, Tag: "native-landing", Type: protocol, ListenPort: 14443, Options: string(raw)}
	serverCache := map[int64]*Server{2: {ID: 2, Host: "127.0.0.1"}}
	tlsCache := map[int64]*SbTls{}
	if tls != nil {
		landing.TlsID = 3
		tlsCache[3] = tls
	}
	beforeTLS, _ := json.Marshal(tls)
	identity := &singbox.User{Name: "fixture-user", UUID: "11111111-1111-1111-1111-111111111111", Password: "fixture:/?#@+ password"}
	var st *Store
	outbound, err := st.relayOutboundWithIdentity(landing, serverCache, tlsCache, identity, "native-outbound")
	if err != nil {
		t.Fatal(err)
	}
	if landing.RelaySecret != "" {
		t.Fatal("explicit identity initialized legacy relay secret")
	}
	afterTLS, _ := json.Marshal(tls)
	if !reflect.DeepEqual(beforeTLS, afterTLS) {
		t.Fatal("renderer mutated cached TLS snapshot")
	}
	if outbound["tag"] != "native-outbound" || outbound["server"] != "127.0.0.1" || outbound["server_port"] != 14443 {
		t.Fatalf("incorrect outbound endpoint: %+v", outbound)
	}
	return outbound
}

func nativeRelayTLSFixture(t *testing.T) *SbTls {
	t.Helper()
	certificate, _, err := singbox.GenerateSelfSignedCert("relay.test", 1)
	if err != nil {
		t.Fatal(err)
	}
	server, _ := json.Marshal(map[string]interface{}{"enabled": true, "server_name": "relay.test", "alpn": []string{"h3", "h2", "http/1.1"}})
	client, _ := json.Marshal(map[string]interface{}{"certificate": certificate, "insecure": false, "utls": map[string]interface{}{"enabled": false}})
	return &SbTls{ID: 3, ServerJSON: string(server), ClientJSON: string(client)}
}

func checkNativeRelayFixture(t *testing.T, outbound map[string]interface{}) {
	t.Helper()
	bin := os.Getenv("QZ_SINGBOX_TEST_BIN")
	if bin == "" {
		return
	}
	raw, err := json.Marshal(map[string]interface{}{"outbounds": []interface{}{outbound}, "route": map[string]interface{}{"final": outbound["tag"]}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "native-relay.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(bin, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box native outbound check: %v\n%s\n%s", err, output, raw)
	}
}

func TestRelayNativeProtocolRendering(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan", "tuic", "hysteria", "hysteria2", "anytls", "shadowsocks-128", "shadowsocks-256"} {
		t.Run(protocol, func(t *testing.T) {
			kind := protocol
			opts := map[string]interface{}{}
			tls := nativeRelayTLSFixture(t)
			switch protocol {
			case "tuic":
				opts["congestion_control"], opts["zero_rtt_handshake"], opts["heartbeat"] = "bbr", true, "8s"
			case "hysteria":
				opts["up_mbps"], opts["down_mbps"], opts["obfs"] = 31, 17, "native xplus /+# password"
			case "hysteria2":
				opts["obfs"] = map[string]interface{}{"type": "salamander", "password": "native salamander password"}
				opts["bbr_profile"], opts["disable_chrome_parrot"] = "conservative", true
			case "shadowsocks-128", "shadowsocks-256":
				kind, tls = "shadowsocks", nil
				method := "2022-blake3-aes-" + strings.TrimPrefix(protocol, "shadowsocks-") + "-gcm"
				opts["method"], opts["password"] = method, singbox.DeriveSSKey("native-server-secret", method)
			}
			outbound := renderNativeRelayFixture(t, kind, opts, tls)
			if outbound["type"] != kind {
				t.Fatalf("protocol was rewritten: %+v", outbound)
			}
			switch kind {
			case "vless", "vmess", "tuic":
				if outbound["uuid"] != "11111111-1111-1111-1111-111111111111" {
					t.Fatal("UUID identity changed")
				}
			case "hysteria":
				if outbound["auth_str"] != "fixture:/?#@+ password" || outbound["up_mbps"] != float64(17) || outbound["down_mbps"] != float64(31) || outbound["obfs"] != opts["obfs"] {
					t.Fatalf("HY1 lost auth, reversed endpoint rates, or XPlus obfs: %+v", outbound)
				}
			case "shadowsocks":
				method := opts["method"].(string)
				want := opts["password"].(string) + ":" + singbox.DeriveSSKey("fixture:/?#@+ password", method)
				if outbound["password"] != want {
					t.Fatal("SS2022 server:user PSK identity changed")
				}
			default:
				if outbound["password"] != "fixture:/?#@+ password" {
					t.Fatal("native password changed")
				}
			}
			if kind == "anytls" {
				for _, field := range []string{"udp_over_tcp", "network", "padding_scheme"} {
					if _, ok := outbound[field]; ok {
						t.Fatalf("AnyTLS has non-native outbound field %s", field)
					}
				}
			}
			if tls != nil {
				tlsOut := outbound["tls"].(map[string]interface{})
				if tlsOut["certificate"] == nil || tlsOut["insecure"] != false || len(tlsOut["alpn"].([]interface{})) != 3 {
					t.Fatalf("TLS client trust or ALPN was lost: %+v", tlsOut)
				}
				if kind == "tuic" || kind == "hysteria" || kind == "hysteria2" {
					if tlsOut["utls"] != nil {
						t.Fatal("QUIC must omit the shared profile's TCP-only uTLS option")
					}
				} else if tlsOut["utls"].(map[string]interface{})["enabled"] != false {
					t.Fatal("TCP profile's explicit disabled uTLS option was lost")
				}
			}
			checkNativeRelayFixture(t, outbound)
		})
	}
}

func TestRelayNativeTransportRendering(t *testing.T) {
	transports := []map[string]interface{}{
		{"type": "ws", "path": "/native-ws", "headers": map[string]interface{}{"Host": []string{"relay.test"}, "X-Native": []string{"one", "two"}}, "max_early_data": 2048, "early_data_header_name": "Sec-WebSocket-Protocol"},
		{"type": "httpupgrade", "host": "relay.test", "path": "/native-upgrade", "headers": map[string]interface{}{"X-Native": []string{"one", "two"}}},
		{"type": "grpc", "service_name": "native-service", "idle_timeout": "15s", "ping_timeout": "5s", "permit_without_stream": true},
		{"type": "http", "host": []string{"relay.test", "other.test"}, "path": "/native-http", "method": "POST", "headers": map[string]interface{}{"X-Native": []string{"one", "two"}}, "idle_timeout": "15s", "ping_timeout": "5s"},
		{"type": "quic"},
	}
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		for _, transport := range transports {
			t.Run(protocol+"-"+transport["type"].(string), func(t *testing.T) {
				outbound := renderNativeRelayFixture(t, protocol, map[string]interface{}{"transport": transport}, nativeRelayTLSFixture(t))
				want, _ := json.Marshal(transport)
				got, _ := json.Marshal(outbound["transport"])
				if string(want) != string(got) {
					t.Fatalf("native transport changed: got %s, want %s", got, want)
				}
				if outbound["flow"] != nil {
					t.Fatal("non-TCP transport acquired Vision")
				}
				checkNativeRelayFixture(t, outbound)
			})
		}
	}
}

func TestRelayNativeHysteriaStringRatesAndGecko(t *testing.T) {
	hy1 := renderNativeRelayFixture(t, "hysteria", map[string]interface{}{"up": "31 Mbps", "down": "17 Mbps", "up_mbps": 11, "down_mbps": 7, "obfs": "xplus"}, nativeRelayTLSFixture(t))
	if hy1["up"] != "17 Mbps" || hy1["down"] != "31 Mbps" || hy1["up_mbps"] != float64(7) || hy1["down_mbps"] != float64(11) {
		t.Fatalf("HY1 endpoint bandwidth was not mirrored: %+v", hy1)
	}
	checkNativeRelayFixture(t, hy1)
	hy2 := renderNativeRelayFixture(t, "hysteria2", map[string]interface{}{"obfs": map[string]interface{}{"type": "gecko", "password": "fixture-gecko", "min_packet_size": 80, "max_packet_size": 120}, "hop_interval": "30s", "hop_interval_max": "45s", "initial_packet_size": 1250, "disable_path_mtu_discovery": true}, nativeRelayTLSFixture(t))
	if hy2["obfs"].(map[string]interface{})["min_packet_size"] != float64(80) || hy2["hop_interval_max"] != "45s" || hy2["initial_packet_size"] != float64(1250) {
		t.Fatalf("HY2 native options lost: %+v", hy2)
	}
	checkNativeRelayFixture(t, hy2)
}

func TestRelayNativeMultiplexAndNetwork(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan", "shadowsocks"} {
		t.Run(protocol, func(t *testing.T) {
			opts := map[string]interface{}{"flow": "none", "tcp_fast_open": true, "tcp_multi_path": true, "multiplex": map[string]interface{}{"enabled": true, "padding": true, "brutal": map[string]interface{}{"enabled": true, "up_mbps": 999, "down_mbps": 999}}}
			tls := nativeRelayTLSFixture(t)
			if protocol == "shadowsocks" {
				tls = nil
				opts["method"], opts["password"], opts["network"] = "2022-blake3-aes-128-gcm", singbox.DeriveSSKey("server", "2022-blake3-aes-128-gcm"), "tcp"
			}
			outbound := renderNativeRelayFixture(t, protocol, opts, tls)
			mux := outbound["multiplex"].(map[string]interface{})
			if mux["enabled"] != true || mux["padding"] != true || mux["brutal"] != nil || outbound["tcp_fast_open"] != true || outbound["tcp_multi_path"] != true {
				t.Fatalf("incorrect relay tuning: %+v", outbound)
			}
			if protocol == "shadowsocks" && outbound["network"] != nil {
				t.Fatal("SS TCP-listener mux must preserve UDP payload capability")
			}
			checkNativeRelayFixture(t, outbound)
		})
	}
}

func TestRelayNativeTLSClientFields(t *testing.T) {
	tls := nativeRelayTLSFixture(t)
	var server, client map[string]interface{}
	_ = json.Unmarshal([]byte(tls.ServerJSON), &server)
	_ = json.Unmarshal([]byte(tls.ClientJSON), &client)
	server["key"], server["key_path"], server["client_authentication"] = "server-private-key", "/private/server.key", "require-and-verify"
	server["ech"] = map[string]interface{}{"enabled": true, "key": "server-private-ech-key"}
	server["min_version"], server["max_version"] = "1.2", "1.3"
	client["server_name"] = "client-override.test"
	client["alpn"] = []string{"http/1.1"}
	delete(client, "certificate") // Native TLS trust anchors and pins are alternatives.
	client["certificate_public_key_sha256"] = []string{base64.StdEncoding.EncodeToString(make([]byte, 32))}
	client["ech"] = map[string]interface{}{"enabled": false}
	client["handshake_timeout"] = "7s"
	sj, _ := json.Marshal(server)
	cj, _ := json.Marshal(client)
	tls.ServerJSON, tls.ClientJSON = string(sj), string(cj)
	outbound := renderNativeRelayFixture(t, "trojan", nil, tls)
	got := outbound["tls"].(map[string]interface{})
	for _, field := range []string{"key", "key_path", "client_authentication"} {
		if got[field] != nil {
			t.Fatalf("server-only TLS field leaked: %s", field)
		}
	}
	if got["server_name"] != "client-override.test" || got["alpn"].([]interface{})[0] != "http/1.1" || got["certificate_public_key_sha256"] == nil || got["min_version"] != "1.2" || got["handshake_timeout"] != "7s" || got["ech"].(map[string]interface{})["enabled"] != false {
		t.Fatalf("native client TLS settings lost: %+v", got)
	}
	checkNativeRelayFixture(t, outbound)
}

func TestRelayNativeInlineTLSAndReality(t *testing.T) {
	private, public, err := singbox.GenerateRealityKeypair()
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		t.Run(protocol, func(t *testing.T) {
			outbound := renderNativeRelayFixture(t, protocol, map[string]interface{}{"tls": map[string]interface{}{"enabled": true, "server_name": "relay.test", "alpn": "http/1.1", "reality": map[string]interface{}{"enabled": true, "private_key": private, "short_id": "1234"}}}, nil)
			tls := outbound["tls"].(map[string]interface{})
			reality := tls["reality"].(map[string]interface{})
			if reality["public_key"] != public || reality["short_id"] != "1234" || reality["private_key"] != nil || tls["alpn"] != "http/1.1" {
				t.Fatalf("inline REALITY was not safely derived: %+v", tls)
			}
			checkNativeRelayFixture(t, outbound)
		})
	}
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		outbound := renderNativeRelayFixture(t, protocol, nil, nil)
		if outbound["tls"] != nil || outbound["flow"] != nil {
			t.Fatalf("plaintext managed %s acquired TLS/Vision", protocol)
		}
		checkNativeRelayFixture(t, outbound)
	}
}

func TestRelayNativeUnavailableTLSFailsClosed(t *testing.T) {
	for _, profile := range []*SbTls{nil, {}, {ServerJSON: "null"}, {DecryptFailed: true}, {ServerJSON: "{"}, {ServerJSON: "{}", ClientJSON: "{"}} {
		var st *Store
		_, err := st.relayOutboundWithIdentity(&SbInbound{ID: 7, Type: "trojan", ListenPort: 443, TlsID: 3}, nil, map[int64]*SbTls{3: profile}, &singbox.User{Password: "fixture"}, "out")
		if err == nil {
			t.Fatalf("unavailable/malformed TLS must fail closed: %+v", profile)
		}
	}
}

func TestRelayNativeHysteria2ForcedBandwidth(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opts     map[string]interface{}
		up, down int
	}{
		{"bbr-default", map[string]interface{}{"up_mbps": 100, "down_mbps": 120}, 0, 0},
		{"forced-bandwidth", map[string]interface{}{"ignore_client_bandwidth": true, "up_mbps": 100, "down_mbps": 120}, 120, 100},
		{"forced-bandwidth-unlimited-send", map[string]interface{}{"ignore_client_bandwidth": true, "down_mbps": 120}, 120, 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outbound := renderNativeRelayFixture(t, "hysteria2", tc.opts, nativeRelayTLSFixture(t))
			if tc.up == 0 {
				if outbound["up_mbps"] != nil || outbound["down_mbps"] != nil {
					t.Fatalf("ordinary HY2 must keep BBR default: %+v", outbound)
				}
			} else if outbound["up_mbps"] != tc.up || outbound["down_mbps"] != tc.down {
				t.Fatalf("HY2 forced-bandwidth client would be rejected: %+v", outbound)
			}
			checkNativeRelayFixture(t, outbound)
		})
	}
}

func TestRelayNativeSelfSignedTrust(t *testing.T) {
	certificate, key, err := singbox.GenerateSelfSignedCert("relay.test", 1)
	if err != nil {
		t.Fatal(err)
	}
	server, _ := json.Marshal(map[string]interface{}{"enabled": true, "server_name": "relay.test", "certificate": []string{certificate}, "key": key})
	tls := &SbTls{ServerJSON: string(server), ClientJSON: `{}`}
	outbound := renderNativeRelayFixture(t, "trojan", nil, tls)
	got := outbound["tls"].(map[string]interface{})
	if got["certificate"] != certificate || got["key"] != nil || got["insecure"] == true {
		t.Fatal("self-signed managed TLS must trust the public certificate without exposing its key or disabling verification")
	}
	checkNativeRelayFixture(t, outbound)
}

func TestRelayNativeShadowsocksListenerNetworks(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		outbound := renderNativeRelayFixture(t, "shadowsocks", map[string]interface{}{"method": "2022-blake3-aes-128-gcm", "password": singbox.DeriveSSKey("server", "2022-blake3-aes-128-gcm"), "network": network}, nil)
		if outbound["network"] != network {
			t.Fatalf("SS without mux must preserve native network=%s", network)
		}
		checkNativeRelayFixture(t, outbound)
	}
}

func TestRelayNativeShadowsocksNetworkListsAndMux(t *testing.T) {
	for _, network := range []interface{}{"tcp", []string{"tcp"}, "udp", []string{"udp"}, []string{}} {
		outbound := renderNativeRelayFixture(t, "shadowsocks", map[string]interface{}{"method": "2022-blake3-aes-128-gcm", "password": singbox.DeriveSSKey("server", "2022-blake3-aes-128-gcm"), "network": network, "multiplex": map[string]interface{}{"enabled": true, "padding": true}}, nil)
		if relayNetworkIncludes(network, "tcp") {
			if outbound["multiplex"] == nil || outbound["network"] != nil {
				t.Fatal("SS TCP-capable mux must carry both payload networks")
			}
		} else {
			if outbound["multiplex"] != nil || outbound["network"] == nil {
				t.Fatal("SS UDP-only listener must use native UDP instead of dialing TCP mux")
			}
		}
		checkNativeRelayFixture(t, outbound)
	}
}

func TestRelayNativeTLSProfileOverridesInline(t *testing.T) {
	outbound := renderNativeRelayFixture(t, "trojan", map[string]interface{}{"tls": map[string]interface{}{"enabled": true, "server_name": "stale-inline.test", "reality": map[string]interface{}{"enabled": true, "private_key": "must-not-survive-profile"}}}, nativeRelayTLSFixture(t))
	tls := outbound["tls"].(map[string]interface{})
	if tls["server_name"] != "relay.test" || tls["reality"] != nil {
		t.Fatalf("profile must replace rather than merge inline TLS: %+v", tls)
	}
	checkNativeRelayFixture(t, outbound)
}

func TestRelayNativeManagedCertificateResolution(t *testing.T) {
	st := newCertTestStore(t)
	certificate, key, err := singbox.GenerateSelfSignedCert("current-cert.test", 1)
	if err != nil {
		t.Fatal(err)
	}
	certID, err := st.SaveCert(&Cert{Name: "managed-relay-cert", Domain: "current-cert.test", Source: "paste", CertPEM: certificate, KeyPEM: key})
	if err != nil {
		t.Fatal(err)
	}
	tlsID, err := st.SaveSbTls(&SbTls{Name: "managed-relay-tls", CertID: certID, ServerJSON: `{"enabled":true,"server_name":"stale-profile.test","certificate_path":"/stale/server.pem","key_path":"/stale/server.key"}`, ClientJSON: `{"insecure":false}`})
	if err != nil {
		t.Fatal(err)
	}
	landing := &SbInbound{ID: 7, Type: "trojan", Tag: "managed-cert-relay", ListenPort: 14443, TlsID: tlsID, Options: `{}`}
	identity := &singbox.User{Password: "fixture-managed-cert"}
	tlsCache := map[int64]*SbTls{}
	outbound, err := st.relayOutboundWithIdentity(landing, map[int64]*Server{}, tlsCache, identity, "managed-cert-outbound")
	if err != nil {
		t.Fatal(err)
	}
	tls := outbound["tls"].(map[string]interface{})
	if tls["server_name"] != "current-cert.test" || tls["certificate"] != certificate || tls["certificate_path"] != nil || tls["key"] != nil || tls["insecure"] == true {
		t.Fatalf("managed relay must use resolved public certificate and SNI: %+v", tls)
	}
	var noDB *Store
	cached, err := noDB.relayOutboundWithIdentity(landing, map[int64]*Server{}, tlsCache, identity, "managed-cert-outbound")
	if err != nil || !reflect.DeepEqual(outbound, cached) {
		t.Fatalf("resolved TLS cache must reproduce managed outbound without DB: %v", err)
	}
	if landing.RelaySecret != "" {
		t.Fatal("managed identity rendering mutated legacy secret")
	}
	checkNativeRelayFixture(t, outbound)
}

func TestRelayNativeQUICTLSCompatibility(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan", "tuic", "hysteria", "hysteria2"} {
		t.Run(protocol, func(t *testing.T) {
			opts := map[string]interface{}{}
			if protocol == "vless" || protocol == "vmess" || protocol == "trojan" {
				opts["transport"] = map[string]interface{}{"type": "quic"}
			}
			if protocol == "hysteria" {
				opts["up_mbps"], opts["down_mbps"] = 100, 120
			}
			profile := nativeRelayTLSFixture(t)
			var client map[string]interface{}
			_ = json.Unmarshal([]byte(profile.ClientJSON), &client)
			client["utls"] = map[string]interface{}{"enabled": true, "fingerprint": "chrome"} // Shared profiles normally carry a TCP fingerprint.
			rawClient, _ := json.Marshal(client)
			profile.ClientJSON = string(rawClient)
			outbound := renderNativeRelayFixture(t, protocol, opts, profile)
			if utls, ok := outbound["tls"].(map[string]interface{})["utls"].(map[string]interface{}); ok && mapBool(utls, "enabled") {
				t.Fatal("QUIC must not inherit the TCP-only default uTLS fingerprint")
			}
			checkNativeRelayFixture(t, outbound)
			for _, incompatible := range []map[string]interface{}{
				{"reality": map[string]interface{}{"enabled": true, "public_key": "explicit-incompatible-key"}},
				{"engine": "apple"},
				{"engine": "windows"},
			} {
				rawOpts, _ := json.Marshal(opts)
				rawClient, _ := json.Marshal(incompatible)
				badProfile := &SbTls{ServerJSON: profile.ServerJSON, ClientJSON: string(rawClient)}
				var st *Store
				_, err := st.relayOutboundWithIdentity(&SbInbound{ID: 7, Type: protocol, ListenPort: 14443, TlsID: 3, Options: string(rawOpts)}, nil, map[int64]*SbTls{3: badProfile}, &singbox.User{UUID: "11111111-1111-1111-1111-111111111111", Password: "fixture"}, "out")
				if err == nil || !strings.Contains(err.Error(), "QUIC") {
					t.Fatalf("QUIC silently accepted a TLS config that cannot provide STDConfig: %s, err=%v", rawClient, err)
				}
			}
		})
	}
}

func TestRelayNativeRequiredTLSFailsClosed(t *testing.T) {
	for _, protocol := range []string{"tuic", "hysteria", "hysteria2", "anytls"} {
		for _, tc := range []struct {
			name, options string
			profile       *SbTls
		}{
			{"missing", `{}`, nil},
			{"inline-disabled", `{"tls":{"enabled":false}}`, nil},
			{"inline-unset-enabled", `{"tls":{"server_name":"relay.test"}}`, nil},
			{"profile-disabled", `{}`, &SbTls{ServerJSON: `{"enabled":false}`, ClientJSON: `{}`}},
			{"profile-unset-enabled", `{}`, &SbTls{ServerJSON: `{"server_name":"relay.test"}`, ClientJSON: `{}`}},
			{"client-disabled", `{}`, &SbTls{ServerJSON: `{"enabled":true}`, ClientJSON: `{"enabled":false}`}},
			{"client-cannot-enable-server", `{}`, &SbTls{ServerJSON: `{"enabled":false}`, ClientJSON: `{"enabled":true}`}},
			{"profile-overrides-enabled-inline", `{"tls":{"enabled":true}}`, &SbTls{ServerJSON: `{"enabled":false}`, ClientJSON: `{}`}},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				landing := &SbInbound{ID: 7, Type: protocol, ListenPort: 14443, Options: tc.options}
				cache := map[int64]*SbTls{}
				if tc.profile != nil {
					landing.TlsID = 3
					cache[3] = tc.profile
				}
				var st *Store
				outbound, err := st.relayOutboundWithIdentity(landing, nil, cache, &singbox.User{UUID: "11111111-1111-1111-1111-111111111111", Password: "fixture"}, "out")
				if err == nil || outbound != nil || !strings.Contains(err.Error(), "requires enabled") {
					t.Fatalf("%s must reject missing/disabled TLS without enabling it: outbound=%+v, err=%v", protocol, outbound, err)
				}
			})
		}
	}
}
