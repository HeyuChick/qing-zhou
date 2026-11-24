package store

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"qingzhou/internal/subconv"
)

func TestSelfBuiltWSShareLinksPreserveTransportAndTLS(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		for _, mode := range []string{"inline", "profile", "plaintext", "ambiguous-inline", "client-override"} {
			t.Run(protocol+"/"+mode, func(t *testing.T) {
				st := newRefundStore(t)
				uid := mkUser(t, st, "ws-export")
				pkg := mkPlan(t, st, "WS", 10, 100, 30)
				buy(t, st, uid, pkg)
				options := map[string]any{"transport": map[string]any{"type": "ws", "path": "/ws?token=a%2Bb&ed=literal", "headers": map[string]any{"host": []string{"cdn.example.test"}, "X-Multiple": []string{"one", "two"}}, "max_early_data": 2048, "early_data_header_name": "Sec-WebSocket-Protocol"}, "flow": "none"}
				tls := map[string]any{"enabled": true, "server_name": "tls.example.test", "alpn": []string{"http/1.1"}}
				var tlsID int64
				var err error
				if mode == "inline" {
					options["tls"] = tls
				}
				if mode == "ambiguous-inline" {
					options["tls"] = "unknown-tls-object"
				}
				if mode == "profile" || mode == "client-override" {
					raw, _ := json.Marshal(tls)
					tlsID, err = st.SaveSbTls(&SbTls{Name: "ws-profile", Mode: "tls", ServerJSON: string(raw), ClientJSON: func() string {
						if mode == "client-override" {
							return `{"insecure":false,"server_name":"client.example.test","alpn":["http/1.1","custom"]}`
						}
						return `{"insecure":false}`
					}()})
					if err != nil {
						t.Fatal(err)
					}
					options["tls"] = map[string]any{"enabled": false}
				}
				raw, _ := json.Marshal(options)
				if _, err = st.SaveSbInbound(&SbInbound{Type: protocol, Tag: "public-ws", ListenPort: 8443, TlsID: tlsID, Enabled: true, Options: string(raw)}); err != nil {
					if mode == "ambiguous-inline" {
						return
					}
					t.Fatal(err)
				}
				bindPlanToInbound(t, st, pkg.ID, "public-ws")
				user, err := st.UserByID(uid)
				if err != nil {
					t.Fatal(err)
				}
				links := st.BuildSelfBuiltLinks(user, "public.example.test")
				if mode == "ambiguous-inline" {
					if len(links) != 0 {
						t.Fatal("ambiguous TLS shape exported")
					}
					return
				}
				if len(links) != 1 {
					t.Fatalf("links=%d", len(links))
				}
				assertPreWSUpgradeNodeKeys(t, links[0], protocol, mode)
				out, err := subconv.SingboxOutboundFromLink(links[0].Link)
				if err != nil {
					t.Fatal(err)
				}
				ws := out["transport"].(map[string]any)
				if ws["path"] != "/ws?token=a%2Bb&ed=literal" || ws["max_early_data"] != 2048 || ws["early_data_header_name"] != "Sec-WebSocket-Protocol" {
					t.Fatal("WS fields lost", ws)
				}
				headers := ws["headers"].(map[string]any)
				if headers["Host"] != "cdn.example.test" || !reflect.DeepEqual(headers["X-Multiple"], []string{"one", "two"}) {
					t.Fatal("header list lost", headers)
				}
				actual, _ := out["tls"].(map[string]any)
				if mode == "plaintext" {
					if actual != nil && actual["enabled"] != false {
						t.Fatal("plaintext misrepresented", actual)
					}
				} else if mode == "client-override" {
					if actual["server_name"] != "client.example.test" || !reflect.DeepEqual(actual["alpn"], []string{"http/1.1", "custom"}) || actual["insecure"] == true {
						t.Fatal("client TLS override changed", actual)
					}
				} else if actual["enabled"] != true || actual["server_name"] != "tls.example.test" || !reflect.DeepEqual(actual["alpn"], []string{"http/1.1"}) || actual["insecure"] == true {
					t.Fatal("TLS profile/inline changed", actual)
				}
				after, err := st.UserByID(uid)
				if err != nil {
					t.Fatal(err)
				}
				if after.ClientUUID != user.ClientUUID || after.ClientSecret != user.ClientSecret {
					t.Fatal("export mutated account credentials")
				}
			})
		}
	}
}

func TestShareLinkTLSStateNeverInfersPlaintextFromUnknown(t *testing.T) {
	cases := []struct {
		id                       int64
		profile, options         map[string]any
		enabled, disabled, valid bool
	}{
		{0, nil, map[string]any{}, false, true, true},
		{0, nil, map[string]any{"tls": map[string]any{"enabled": true}}, true, false, true},
		{0, nil, map[string]any{"tls": map[string]any{"enabled": false}}, false, true, true},
		{0, nil, map[string]any{"tls": map[string]any{}}, false, false, false},
		{0, nil, map[string]any{"tls": true}, false, false, false},
		{1, nil, map[string]any{}, true, false, true},
		{1, map[string]any{"enabled": true}, map[string]any{"tls": map[string]any{"enabled": false}}, true, false, true},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			e, d, _, v := shareLinkTLSState(tc.id, tc.profile, tc.options)
			if e != tc.enabled || d != tc.disabled || v != tc.valid {
				t.Fatalf("got %v/%v/%v", e, d, v)
			}
		})
	}
}

func assertPreWSUpgradeNodeKeys(t *testing.T, link SelfBuiltLink, protocol, mode string) {
	t.Helper()
	p, err := subconv.ParseLink(link.Link)
	if err != nil {
		t.Fatal(err)
	}
	profile := mode == "profile" || mode == "client-override"
	oldSNI := ""
	if profile {
		oldSNI = "tls.example.test"
	}
	oldPath := "/ws?token=a%2Bb&ed=literal"
	oldLink := ""
	if protocol == "vmess" {
		fields := map[string]any{"v": "2", "ps": p.Name, "add": "public.example.test", "port": "8443", "id": p.UUID, "aid": "0", "scy": "auto", "type": "none", "net": "ws", "host": oldSNI, "path": oldPath, "tls": ""}
		if profile {
			fields["tls"] = "tls"
			fields["sni"] = oldSNI
			fields["fp"] = "chrome"
			fields["alpn"] = "http/1.1"
		}
		raw, _ := json.Marshal(fields)
		oldLink = "vmess://" + base64.StdEncoding.EncodeToString(raw)
	} else {
		credential := p.UUID
		if protocol == "trojan" {
			credential = p.Password
		}
		q := []string{"type=ws", "path=" + url.QueryEscape(oldPath)}
		if oldSNI != "" {
			q = append(q, "host="+url.QueryEscape(oldSNI))
		}
		q = append(q, "max_early_data=2048", "early_data_header_name=Sec-WebSocket-Protocol", "security=tls", "fp=chrome", "sni="+url.QueryEscape(oldSNI))
		if protocol == "vless" {
			q = append(q, "packetEncoding=xudp")
		}
		oldLink = protocol + "://" + url.QueryEscape(credential) + "@public.example.test:8443?" + strings.Join(q, "&") + "#" + url.QueryEscape(p.Name)
	}
	actual := map[string]bool{}
	for _, key := range link.LegacyKeys {
		actual[key] = true
	}
	for _, key := range subconv.NodeKeys(oldLink) {
		if !actual[key] {
			t.Fatalf("old serializer key missing for %s/%s: %s in %v", protocol, mode, key, link.LegacyKeys)
		}
	}
}
