package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

func sampleLocal(t *testing.T, st *store.Store, cpu float64) {
	t.Helper()
	if err := st.InsertMetrics(store.LocalNodeID, store.ServerMetrics{
		CPUPercent: cpu, MemUsed: 1 << 30, MemTotal: 2 << 30,
		DiskUsed: 5 << 30, DiskTotal: 20 << 30, Hostname: "panel-host",
	}); err != nil {
		t.Fatal(err)
	}
}

func getJSON(t *testing.T, h http.HandlerFunc, target string, out any) {
	t.Helper()
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", target, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s: status %d, body %s", target, w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatalf("%s: decode %s: %v", target, w.Body.String(), err)
	}
}

// The panel's own machine has no servers row and never will — an SSH-managed
// row for it would make sbctl deploy sing-box to it twice, over SSH and
// in-process. So it is injected into the monitor list from its metrics, and the
// admin never has to add a server just to watch the machine already running the
// panel.
func TestLocalNodeAppearsInMonitorList(t *testing.T) {
	a, st := newNodeUpgradeAPI(t)

	var before struct {
		Data []struct {
			ID    int64 `json:"id"`
			Local bool  `json:"local"`
		} `json:"data"`
	}
	getJSON(t, a.handleMonitorServers, "/api/admin/monitor/servers", &before)
	if len(before.Data) != 0 {
		t.Fatalf("nothing has been sampled yet, so nothing should be listed: %+v", before.Data)
	}

	sampleLocal(t, st, 12.5)

	var after struct {
		Data []struct {
			ID            int64  `json:"id"`
			Name          string `json:"name"`
			Local         bool   `json:"local"`
			ProbeEnabled  bool   `json:"probe_enabled"`
			ProbeToken    string `json:"probe_token"`
			PublicVisible bool   `json:"public_visible"`
			Status        string `json:"status"`
			Metrics       *struct {
				CPUPercent float64 `json:"cpu_percent"`
			} `json:"metrics"`
		} `json:"data"`
	}
	getJSON(t, a.handleMonitorServers, "/api/admin/monitor/servers", &after)
	if len(after.Data) != 1 {
		t.Fatalf("want exactly the local machine, got %+v", after.Data)
	}
	row := after.Data[0]
	if row.ID != store.LocalNodeID || !row.Local || row.Name != store.LocalNodeName {
		t.Fatalf("row = %+v, want the local node", row)
	}
	if row.Metrics == nil || row.Metrics.CPUPercent != 12.5 {
		t.Fatalf("metrics = %+v, want the sample just written", row.Metrics)
	}
	// No token: the UI keys "show the probe install command" off this, and the
	// local machine needs no probe at all.
	if row.ProbeToken != "" {
		t.Fatalf("local node must not carry a probe token, got %q", row.ProbeToken)
	}
	if !row.ProbeEnabled || row.Status != "online" {
		t.Fatalf("local node should read as a live, monitored machine: %+v", row)
	}
}

// The status page is unauthenticated. The panel host is the one machine whose
// name says exactly what it is, so it stays off that page until an admin says
// otherwise — while every other machine keeps the behaviour it had before the
// flag existed, or an upgrade would silently empty someone's status page.
func TestPublicPageVisibilityDefaults(t *testing.T) {
	a, st := newNodeUpgradeAPI(t)
	sampleLocal(t, st, 20)
	id, err := st.CreateServer(store.Server{Name: "landing-1", Host: "192.0.2.10", ProbeEnabled: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMetrics(id, store.ServerMetrics{CPUPercent: 30}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchProbeSeen(id); err != nil {
		t.Fatal(err)
	}

	names := func() []string {
		var body struct {
			Data struct {
				Servers []struct {
					Name string `json:"name"`
				} `json:"servers"`
			} `json:"data"`
		}
		getJSON(t, a.handleMonitorPublic, "/api/monitor/public", &body)
		var out []string
		for _, s := range body.Data.Servers {
			out = append(out, s.Name)
		}
		return out
	}

	got := names()
	if len(got) != 1 || got[0] != "landing-1" {
		t.Fatalf("public page = %v; want the landing node only (local hidden by default)", got)
	}

	// Admin opts the panel host in.
	setVisible(t, a, store.LocalNodeID, true)
	if got := names(); len(got) != 2 {
		t.Fatalf("public page = %v; want the local machine listed once opted in", got)
	}

	// And opts the landing node out.
	setVisible(t, a, id, false)
	got = names()
	if len(got) != 1 || got[0] != store.LocalNodeName {
		t.Fatalf("public page = %v; want only the local machine after hiding the landing node", got)
	}
}

func setVisible(t *testing.T, a *API, id int64, v bool) {
	t.Helper()
	body := `{"public_visible":` + map[bool]string{true: "true", false: "false"}[v] + `}`
	req := httptest.NewRequest("PUT", "/x", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(id, 10))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	a.handleUpdateServerMonitor(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set public_visible=%v on %d: status %d, body %s", v, id, w.Code, w.Body.String())
	}
}

// The heatmap is built once for two audiences. Getting the filter backwards
// there would publish, in outline and by name, exactly the machines the flag
// exists to keep off the public page.
func TestHeatmapRespectsPublicVisibility(t *testing.T) {
	a, st := newNodeUpgradeAPI(t)
	sampleLocal(t, st, 5) // local: hidden from the public page by default

	rows := func(h http.HandlerFunc, path string) int {
		var body struct {
			Data struct {
				Servers []struct {
					Name string `json:"name"`
				} `json:"servers"`
			} `json:"data"`
		}
		getJSON(t, h, path, &body)
		return len(body.Data.Servers)
	}

	if n := rows(a.handleMonitorHeatmap, "/api/admin/monitor/heatmap"); n != 1 {
		t.Fatalf("admin heatmap rows = %d, want 1 (the local machine)", n)
	}
	if n := rows(a.handleMonitorPublicHeatmap, "/api/monitor/heatmap"); n != 0 {
		t.Fatalf("public heatmap rows = %d, want 0 while the local machine is hidden", n)
	}

	setVisible(t, a, store.LocalNodeID, true)
	if n := rows(a.handleMonitorPublicHeatmap, "/api/monitor/heatmap"); n != 1 {
		t.Fatalf("public heatmap rows = %d, want 1 once opted in", n)
	}
}
