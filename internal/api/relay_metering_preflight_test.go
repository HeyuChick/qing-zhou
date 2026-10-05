package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qingzhou/internal/sbctl"
	"qingzhou/internal/sbver"
	"qingzhou/internal/store"
)

func TestRelayMeteringPreflightIsReadOnlyAndNamesAffectedNodes(t *testing.T) {
	a, st := newUserEditAPI(t)
	a.sbctl = new(sbctl.Controller)
	id, err := st.CreateServer(store.Server{Name: "预检落地", Host: "192.0.2.3", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateServer(store.Server{Name: "已禁用节点", Host: "192.0.2.4", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetNodeSingbox(id, sbver.Info{Version: "1.14.2", HasV2RayAPI: true}); err != nil {
		t.Fatal(err)
	}
	var before int
	if err = st.DB().QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.handleRelayMeteringPreflight(w, httptest.NewRequest(http.MethodGet, "/api/admin/relay-metering/preflight?enabled=false&cumulative_enabled=false&per_user_enabled=false", nil))
	if w.Code != 200 {
		t.Fatalf("preflight %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Data struct {
			Valid  bool                         `json:"valid"`
			Errors []string                     `json:"errors"`
			Nodes  []relayMeteringPreflightNode `json:"nodes"`
		}
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Data.Valid || len(response.Data.Errors) > 0 || len(response.Data.Nodes) != 2 {
		t.Fatalf("unexpected preflight: %+v", response.Data)
	}
	if response.Data.Nodes[0].Name != store.LocalNodeName || response.Data.Nodes[1].Name != "预检落地" || response.Data.Nodes[1].Version != "1.14.2" || !response.Data.Nodes[1].HasV2RayAPI {
		t.Fatalf("wrong affected nodes: %+v", response.Data.Nodes)
	}
	var after int
	if err = st.DB().QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after || st.RelayMeteringEnabled() || st.RelayUserMeteringEnabled() || len(a.sbctl.SyncStatuses()) != 0 {
		t.Fatal("read-only preflight mutated settings, credentials or scheduler")
	}
	if strings.Contains(w.Body.String(), "192.0.2.3") {
		t.Fatal("preflight needlessly exposes connection data")
	}
}

func TestRelayMeteringPreflightListsValidationAndControllerFailures(t *testing.T) {
	a, _ := newUserEditAPI(t)
	w := httptest.NewRecorder()
	a.handleRelayMeteringPreflight(w, httptest.NewRequest(http.MethodGet, "/api/admin/relay-metering/preflight?enabled=false&cumulative_enabled=false&per_user_enabled=true", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "控制器未启用") || !strings.Contains(w.Body.String(), "需要保持中转链路计量启用") || !strings.Contains(w.Body.String(), `"valid":false`) {
		t.Fatalf("missing errors: %d %s", w.Code, w.Body.String())
	}
}

func TestRelayMeteringPreflightRejectsMalformedFlags(t *testing.T) {
	a, _ := newUserEditAPI(t)
	for _, query := range []string{"", "?enabled=true&cumulative_enabled=no&per_user_enabled=false", "?enabled=false&cumulative_enabled=false"} {
		w := httptest.NewRecorder()
		a.handleRelayMeteringPreflight(w, httptest.NewRequest(http.MethodGet, "/api/admin/relay-metering/preflight"+query, nil))
		if w.Code != 400 {
			t.Fatalf("malformed query accepted: %s %d", query, w.Code)
		}
	}
}

func TestRelayMeteringCapabilityFailuresStaySpecificAndUnknownIsNotUnsupported(t *testing.T) {
	a, st := newUserEditAPI(t)
	id, err := st.CreateServer(store.Server{Name: "未知节点", Host: "192.0.2.7", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetNodeSingboxError(id, "SSH refused"); err != nil {
		t.Fatal(err)
	}
	nodes, err := a.relayMeteringPreflightNodes(true, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if node.ServerID == id {
			if !node.RequiresCheck || node.RequiresReinstall || node.Error != "SSH refused" {
				t.Fatalf("unknown capability mislabeled: %+v", node)
			}
			return
		}
	}
	t.Fatal("missing failed node")
}

func TestGetRelayMeteringProvidesControllerEpochAndReadOnlyNodeNames(t *testing.T) {
	a, _ := newUserEditAPI(t)
	a.sbctl = new(sbctl.Controller)
	w := httptest.NewRecorder()
	a.handleGetRelayMetering(w, httptest.NewRequest(http.MethodGet, "/api/admin/relay-metering", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sync_epoch":"`+a.sbctl.SyncEpoch()) || !strings.Contains(w.Body.String(), store.LocalNodeName) {
		t.Fatalf("status metadata absent: %d %s", w.Code, w.Body.String())
	}
}
