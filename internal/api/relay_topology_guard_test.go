package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qingzhou/internal/store"
)

func TestRelayTopologyRejectedSavesReturnActionableError(t *testing.T) {
	for _, action := range []string{"inbound-disable", "inbound-delete", "server-disable", "tls-edit", "logical-create"} {
		t.Run(action, func(t *testing.T) {
			a, st := newHostKeyAPI(t)
			st.SetSecretKey([]byte("topology-api-fixture"))
			src, err := st.CreateServer(store.Server{Name: "entry", Host: "192.0.2.10", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			dst, err := st.CreateServer(store.Server{Name: "exit", Host: "192.0.2.11", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			tlsID, err := st.SaveSbTls(&store.SbTls{Name: "test-tls", ServerJSON: `{"enabled":true}`, ClientJSON: `{}`})
			if err != nil {
				t.Fatal(err)
			}
			target, err := st.SaveSbInbound(&store.SbInbound{ServerID: dst, Type: "tuic", Tag: "exit", ListenPort: 2443, TlsID: tlsID, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			_, err = st.SaveSbInbound(&store.SbInbound{ServerID: src, Type: "vless", Tag: "entry", ListenPort: 2443, Enabled: true, UpstreamInboundID: target})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.ConfigureTrafficMetering(true, false, true); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			switch action {
			case "inbound-disable":
				a.handleAdminSaveSbInbound(rec, withID(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"enabled":false}`)), target))
			case "inbound-delete":
				a.handleAdminDeleteSbInbound(rec, withID(httptest.NewRequest(http.MethodDelete, "/", nil), target))
			case "server-disable":
				a.handleAdminUpdateServer(rec, withID(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"enabled":false}`)), dst))
			case "tls-edit":
				a.handleAdminSaveSbTls(rec, withID(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"name":"rejected","server_json":"{\"enabled\":false}","client_json":"{}"}`)), tlsID))
			case "logical-create":
				// Two distinct inbounds on a single server pass the old per-node
				// inbound-cycle precheck but cannot be staged by machine.
				other, err := st.SaveSbInbound(&store.SbInbound{ServerID: src, Type: "vless", Tag: "other", ListenPort: 2444, Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				a.handleAdminCreateNode(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(fmt.Sprintf(`{"type":"self_built","name":"same-machine","inbound_tag":"entry","route_upstream_inbound_id":%d,"enabled":true}`, other))))
			}
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "中转配置未保存") {
				t.Fatalf("generic/incorrect rejection: status=%d body=%s", rec.Code, rec.Body.String())
			}
			ib, err := st.GetSbInbound(target)
			if err != nil || ib == nil || !ib.Enabled {
				t.Fatalf("rejected API action changed target: %v", err)
			}
		})
	}
}

func TestLogicalRouteAPIAllRelayLandingProtocols(t *testing.T) {
	for _, protocol := range []string{"anytls", "hysteria", "mixed"} {
		t.Run(protocol, func(t *testing.T) {
			a, st := newHostKeyAPI(t)
			st.SetSecretKey([]byte("logical-protocol-api-fixture"))
			src, err := st.CreateServer(store.Server{Name: "entry", Host: "192.0.2.10", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			dst, err := st.CreateServer(store.Server{Name: "exit", Host: "192.0.2.11", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			tlsID, err := st.SaveSbTls(&store.SbTls{Name: "protocol-tls", ServerJSON: `{"enabled":true}`, ClientJSON: `{}`})
			if err != nil {
				t.Fatal(err)
			}
			_, err = st.SaveSbInbound(&store.SbInbound{ServerID: src, Type: "vless", Tag: "logical-entry", ListenPort: 2443, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			target, err := st.SaveSbInbound(&store.SbInbound{ServerID: dst, Type: protocol, Tag: "logical-exit", ListenPort: 2443, TlsID: tlsID, Enabled: true, Options: `{"up_mbps":100,"down_mbps":100}`})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.ConfigureTrafficMetering(true, false, true); err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"type":"self_built","name":"logical-route","inbound_tag":"logical-entry","route_upstream_inbound_id":%d,"enabled":true}`, target)
			create := httptest.NewRecorder()
			a.handleAdminCreateNode(create, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
			if protocol == "mixed" {
				if create.Code != http.StatusBadRequest {
					t.Fatalf("mixed landing accepted: %d %s", create.Code, create.Body.String())
				}
				return
			}
			if create.Code != http.StatusOK {
				t.Fatalf("supported %s route rejected: %d %s", protocol, create.Code, create.Body.String())
			}
			nodes, err := st.ListNodes()
			if err != nil || len(nodes) != 1 || nodes[0].RouteUpstreamInboundID != target {
				t.Fatalf("logical route was not saved: %v", err)
			}
			update := httptest.NewRecorder()
			a.handleAdminUpdateNode(update, withID(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)), nodes[0].ID))
			if update.Code != http.StatusOK {
				t.Fatalf("supported %s route update rejected: %d %s", protocol, update.Code, update.Body.String())
			}
		})
	}
}
