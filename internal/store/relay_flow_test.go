package store

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"qingzhou/internal/singbox"
)

// All certificates, keys, endpoints and configurations are isolated fixtures.
// Optional `check` validates syntax/features only; it is not a traffic test.
func TestRelayVLESSFlowMatchesListener(t *testing.T) {
	for _, tc := range []struct {
		name, tls, options, wantFlow string
	}{
		{"plaintext", "", `{"flow":"vision"}`, ""},
		{"tls-default-vision", "tls", `{}`, "xtls-rprx-vision"},
		{"tls-explicit-vision", "tls", `{"flow":"vision"}`, "xtls-rprx-vision"},
		{"tls-empty-transport", "tls", `{"transport":{}}`, "xtls-rprx-vision"},
		{"tls-flow-none", "tls", `{"flow":"none","multiplex":{"enabled":true}}`, ""},
		{"ws-tls", "tls", `{"flow":"vision","transport":{"type":"ws","path":"/fixture"}}`, ""},
		{"reality-vision", "reality", `{}`, "xtls-rprx-vision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newRefundStore(t)
			st.SetSecretKey([]byte("flow-fixture-only"))
			serverID, err := st.CreateServer(Server{Name: "fixture-landing", Host: "127.0.0.1", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			var tlsID int64
			if tc.tls != "" {
				server := map[string]interface{}{"enabled": true, "server_name": "relay.test"}
				client := map[string]interface{}{"insecure": false}
				if tc.tls == "reality" {
					private, public, err := singbox.GenerateRealityKeypair()
					if err != nil {
						t.Fatal(err)
					}
					server["reality"] = map[string]interface{}{"enabled": true, "private_key": private, "short_id": []string{"0123456789abcdef"}, "handshake": map[string]interface{}{"server": "127.0.0.1", "server_port": 443}}
					client["reality"] = map[string]interface{}{"enabled": true, "public_key": public, "short_id": "0123456789abcdef"}
					client["utls"] = map[string]interface{}{"enabled": true, "fingerprint": "chrome"}
				} else {
					certificate, key, err := singbox.GenerateSelfSignedCert("relay.test", 1)
					if err != nil {
						t.Fatal(err)
					}
					server["certificate"], server["key"] = certificate, key
				}
				serverJSON, err := json.Marshal(server)
				if err != nil {
					t.Fatal(err)
				}
				clientJSON, err := json.Marshal(client)
				if err != nil {
					t.Fatal(err)
				}
				tlsID, err = st.SaveSbTls(&SbTls{ServerID: serverID, Name: "fixture TLS", Mode: tc.tls, ServerJSON: string(serverJSON), ClientJSON: string(clientJSON)})
				if err != nil {
					t.Fatal(err)
				}
			}
			landingID, err := st.SaveSbInbound(&SbInbound{ServerID: serverID, Type: "vless", Tag: "flow-landing", Listen: "127.0.0.1", ListenPort: 2443, TlsID: tlsID, Options: tc.options, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			landing, err := st.GetSbInbound(landingID)
			if err != nil {
				t.Fatal(err)
			}
			wire := singbox.User{Name: "flow-fixture", UUID: "11111111-1111-1111-1111-111111111111", Password: "fixture-password"}
			outbound, err := st.relayOutboundWithIdentity(landing, map[int64]*Server{}, map[int64]*SbTls{}, &wire, "flow-outbound")
			if err != nil {
				t.Fatal(err)
			}
			actualFlow, _ := outbound["flow"].(string)
			if actualFlow != tc.wantFlow {
				t.Fatalf("outbound flow=%q want=%q", actualFlow, tc.wantFlow)
			}
			if tc.wantFlow != "" && outbound["multiplex"] != nil {
				t.Fatal("Vision outbound retained multiplex")
			}
			tls, _ := outbound["tls"].(map[string]interface{})
			if (tls["enabled"] == true) != (tc.tls != "") || tls["insecure"] == true {
				t.Fatalf("TLS presence/trust changed: %+v", tls)
			}
			raw, err := st.BuildSingboxConfigForServer(serverID, singbox.DefaultBaseConfig, "", map[string][]singbox.User{landing.Tag: {wire}})
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]interface{}
			if err = json.Unmarshal(raw, &config); err != nil {
				t.Fatal(err)
			}
			inbound := config["inbounds"].([]interface{})[0].(map[string]interface{})
			actualUser := inbound["users"].([]interface{})[0].(map[string]interface{})
			inboundFlow, _ := actualUser["flow"].(string)
			if inboundFlow != actualFlow {
				t.Fatalf("inbound/outbound flow mismatch: %q/%q", inboundFlow, actualFlow)
			}
			config["outbounds"] = append(config["outbounds"].([]interface{}), outbound)
			checked, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if bin := os.Getenv("QZ_SINGBOX_TEST_BIN"); bin != "" {
				path := filepath.Join(t.TempDir(), "flow.json")
				if err = os.WriteFile(path, checked, 0600); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command(bin, "check", "-c", path).CombinedOutput(); err != nil {
					t.Fatalf("sing-box check: %v %s", err, out)
				}
			}
		})
	}
}

func TestRelayVisionDropsOutboundMultiplex(t *testing.T) {
	st := newRefundStore(t)
	tlsID, err := st.SaveSbTls(&SbTls{Name: "vision fixture", ServerJSON: `{"enabled":true,"server_name":"relay.test"}`, ClientJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.SaveSbInbound(&SbInbound{Type: "vless", Tag: "vision-fixture", ListenPort: 2443, TlsID: tlsID, Options: `{"flow":"vision","multiplex":{"enabled":true}}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	inbound, err := st.GetSbInbound(id)
	if err != nil {
		t.Fatal(err)
	}
	outbound, err := st.relayOutboundWithIdentity(inbound, map[int64]*Server{}, map[int64]*SbTls{}, nil, "vision-out")
	if err != nil {
		t.Fatal(err)
	}
	if outbound["flow"] != "xtls-rprx-vision" || outbound["multiplex"] != nil {
		t.Fatalf("Vision must take precedence over incompatible outbound multiplex: %+v", outbound)
	}
}
