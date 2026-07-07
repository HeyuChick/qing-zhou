package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/idgen"
	"qingzhou/internal/store"
)

// ---- Agent report (public, token-authenticated) ----

// handleMonitorReport receives metrics from a probe agent. Authenticates via
// the probe_token in the Authorization: Bearer header.
func (a *API) handleMonitorReport(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	tok = strings.TrimSpace(tok)
	if tok == "" {
		fail(w, 401, "缺少认证 token")
		return
	}

	sv, err := a.st.GetServerByProbeToken(tok)
	if err != nil {
		fail(w, 500, "服务器查询失败")
		return
	}
	if sv == nil {
		fail(w, 403, "无效的 token 或探针未启用")
		return
	}

	var m store.ServerMetrics
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		fail(w, 400, "请求格式错误")
		return
	}

	if err := a.st.InsertMetrics(sv.ID, m); err != nil {
		fail(w, 500, "写入指标失败")
		return
	}

	_ = a.st.TouchProbeSeen(sv.ID)
	_ = a.st.UpdateServerStatus(sv.ID, "online")

	ok(w, J{"ok": true})
}

// handleDownloadAgent serves the pre-compiled probe agent binary.
func (a *API) handleDownloadAgent(w http.ResponseWriter, r *http.Request) {
	arch := chi.URLParam(r, "arch") // linux-amd64 or linux-arm64
	if arch != "linux-amd64" && arch != "linux-arm64" {
		fail(w, 400, "不支持的架构，可选: linux-amd64, linux-arm64")
		return
	}

	// Try QZ_PROBE_DIR first, then default path.
	probeDir := os.Getenv("QZ_PROBE_DIR")
	if probeDir == "" {
		probeDir = "cmd/probe/dist"
	}
	path := probeDir + "/probe-" + arch

	http.ServeFile(w, r, path)
}

// handleDownloadInstallScript serves a one-click install script that downloads
// the probe binary and sets up systemd. Usage: bash <(curl -sL <panel>/api/monitor/install.sh) <token>
func (a *API) handleDownloadInstallScript(w http.ResponseWriter, r *http.Request) {
	panelURL := publicBase(r)
	script := `#!/bin/bash
# qingzhou-probe one-click installer
# Usage: bash <(curl -sL ` + panelURL + `/api/monitor/install.sh) <probe_token>
set -e
TOKEN="${1:-}"
if [ -z "$TOKEN" ]; then
  echo "用法: bash install.sh <probe_token>"
  echo "Token 在面板「服务器监控」页面获取"
  exit 1
fi
ARCH=$(uname -m)
case $ARCH in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *) echo "不支持的架构: $ARCH"; exit 1 ;;
esac
echo "下载探针二进制 (${ARCH})..."
curl -sL "` + panelURL + `/api/monitor/agent/linux-${ARCH}" -o /usr/local/bin/qingzhou-probe
chmod +x /usr/local/bin/qingzhou-probe
cat > /etc/qingzhou-probe.env << EOF
QZ_PROBE_SERVER=` + panelURL + `
QZ_PROBE_TOKEN=${TOKEN}
EOF
chmod 600 /etc/qingzhou-probe.env
cat > /etc/systemd/system/qingzhou-probe.service << 'EOF'
[Unit]
Description=Qingzhou Monitor Probe
After=network.target
[Service]
Type=simple
EnvironmentFile=/etc/qingzhou-probe.env
ExecStart=/usr/local/bin/qingzhou-probe
Restart=always
RestartSec=10
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now qingzhou-probe
echo "✅ 探针安装完成！"
`
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"install-probe.sh\"")
	w.Write([]byte(script))
}

// ---- Admin monitoring endpoints ----

func (a *API) handleMonitorDashboard(w http.ResponseWriter, r *http.Request) {
	total, online, expiring, err := a.st.CountProbeServers()
	if err != nil {
		fail(w, 500, "查询失败")
		return
	}
	unread, _ := a.st.UnreadAlertCount()

	// Aggregate latest metrics across all probe servers.
	views, _ := a.st.ListProbeServersWithMetrics()
	var totalCPU float64
	var totalMemUsed, totalMemTotal, totalDiskUsed, totalDiskTotal int64
	var count int
	for _, v := range views {
		if v.Metrics == nil {
			continue
		}
		totalCPU += v.Metrics.CPUPercent
		totalMemUsed += v.Metrics.MemUsed
		totalMemTotal += v.Metrics.MemTotal
		totalDiskUsed += v.Metrics.DiskUsed
		totalDiskTotal += v.Metrics.DiskTotal
		count++
	}

	ok(w, J{
		"total_servers":  total,
		"online":         online,
		"offline":        total - online,
		"expiring_soon":  expiring,
		"alerts_unread":  unread,
		"summary": J{
			"total_cpu":       totalCPU,
			"total_mem_used":  totalMemUsed,
			"total_mem_total": totalMemTotal,
			"total_disk_used": totalDiskUsed,
			"total_disk_total": totalDiskTotal,
		},
	})
}

func (a *API) handleMonitorServers(w http.ResponseWriter, r *http.Request) {
	// Return ALL servers (not just probe-enabled) so the UI can manage probes.
	servers, err := a.st.ListServers()
	if err != nil {
		fail(w, 500, "查询失败")
		return
	}

	type serverResp struct {
		ID          int64               `json:"id"`
		Name        string              `json:"name"`
		Host        string              `json:"host"`
		Enabled     bool                `json:"enabled"`
		ProbeEnabled bool               `json:"probe_enabled"`
		ProbeToken  string              `json:"probe_token"`
		Provider    string              `json:"provider"`
		Location    string              `json:"location"`
		Spec        string              `json:"spec"`
		Price       float64             `json:"price"`
		ExpiryDate  int64               `json:"expiry_date"`
		DaysLeft    *int                `json:"days_left"`
		Status      string              `json:"status"`
		LastSeen    int64               `json:"last_seen"`
		Metrics     *store.ServerMetrics `json:"metrics"`
		Notes       string              `json:"notes"`
	}

	now := time.Now()
	onlineWindow := now.Add(-2 * time.Minute).Unix()
	var out []serverResp
	for _, sv := range servers {
		maskServerSecrets(sv)
		status := "offline"
		if sv.LastSeen >= onlineWindow {
			status = "online"
		}
		var m *store.ServerMetrics
		if sv.ProbeEnabled {
			m, _ = a.st.GetLatestMetrics(sv.ID)
		}
		var dl *int
		if sv.ExpiryDate > 0 {
			d := int(time.Unix(sv.ExpiryDate, 0).Sub(now).Hours() / 24)
			if d < 0 {
				d = 0
			}
			dl = &d
		}
		out = append(out, serverResp{
			ID:          sv.ID,
			Name:        sv.Name,
			Host:        sv.Host,
			Enabled:     sv.Enabled,
			ProbeEnabled: sv.ProbeEnabled,
			ProbeToken:  sv.ProbeToken,
			Provider:    sv.Provider,
			Location:    sv.Location,
			Spec:        sv.Spec,
			Price:       sv.Price,
			ExpiryDate:  sv.ExpiryDate,
			DaysLeft:    dl,
			Status:      status,
			LastSeen:    sv.LastSeen,
			Metrics:     m,
			Notes:       sv.Notes,
		})
	}
	if out == nil {
		out = []serverResp{}
	}
	ok(w, out)
}

func (a *API) handleServerMetrics(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	rangeStr := r.URL.Query().Get("range")
	if rangeStr == "" {
		rangeStr = "24h"
	}

	var since time.Duration
	switch rangeStr {
	case "1h":
		since = time.Hour
	case "6h":
		since = 6 * time.Hour
	case "24h":
		since = 24 * time.Hour
	case "7d":
		since = 7 * 24 * time.Hour
	case "30d":
		since = 30 * 24 * time.Hour
	default:
		since = 24 * time.Hour
	}

	sinceTs := time.Now().Add(-since).Unix()
	data, err := a.st.ListMetrics(id, sinceTs)
	if err != nil {
		fail(w, 500, "查询指标失败")
		return
	}
	if data == nil {
		data = []*store.ServerMetrics{}
	}
	ok(w, J{
		"server_id": id,
		"range":     rangeStr,
		"data":      data,
	})
}

func (a *API) handleMonitorAlerts(w http.ResponseWriter, r *http.Request) {
	unreadOnly := r.URL.Query().Get("unread") == "1"
	alerts, err := a.st.ListAlerts(unreadOnly)
	if err != nil {
		fail(w, 500, "查询告警失败")
		return
	}
	if alerts == nil {
		alerts = []*store.ServerAlert{}
	}
	ok(w, alerts)
}

func (a *API) handleMarkAlertRead(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if err := a.st.MarkAlertRead(id); err != nil {
		fail(w, 500, "标记失败")
		return
	}
	ok(w, nil)
}

func (a *API) handleUpdateServerMonitor(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	sv, err := a.st.GetServer(id)
	if sv == nil {
		fail(w, 404, "服务器不存在")
		return
	}
	if err != nil {
		fail(w, 500, "读取服务器失败")
		return
	}

	// Partial update: only monitor-related fields.
	var body struct {
		ProbeEnabled *bool    `json:"probe_enabled"`
		ExpiryDate   *int64   `json:"expiry_date"`
		Provider     *string  `json:"provider"`
		Location     *string  `json:"location"`
		Spec         *string  `json:"spec"`
		Price        *float64 `json:"price"`
		Notes        *string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "请求格式错误")
		return
	}

	if body.ProbeEnabled != nil {
		sv.ProbeEnabled = *body.ProbeEnabled
		if sv.ProbeEnabled && sv.ProbeToken == "" {
			// Auto-generate probe token when enabling.
			tok, terr := idgen.RandToken(24)
			if terr != nil {
				fail(w, 500, "生成 token 失败")
				return
			}
			sv.ProbeToken = tok
		}
	}
	if body.ExpiryDate != nil {
		sv.ExpiryDate = *body.ExpiryDate
	}
	if body.Provider != nil {
		sv.Provider = *body.Provider
	}
	if body.Location != nil {
		sv.Location = *body.Location
	}
	if body.Spec != nil {
		sv.Spec = *body.Spec
	}
	if body.Price != nil {
		sv.Price = *body.Price
	}
	if body.Notes != nil {
		sv.Notes = *body.Notes
	}

	if err := a.st.UpdateServer(*sv); err != nil {
		fail(w, 500, "更新失败")
		return
	}

	saved, _ := a.st.GetServer(id)
	maskServerSecrets(saved)
	ok(w, saved)
}

// ---- Public monitoring (no auth required) ----

// handleMonitorPublic returns a sanitized view of probe-enabled servers for the
// public monitoring dashboard. No authentication required; sensitive fields
// (SSH keys, probe tokens, host IPs) are excluded.
func (a *API) handleMonitorPublic(w http.ResponseWriter, r *http.Request) {
	servers, err := a.st.ListServers()
	if err != nil {
		fail(w, 500, "查询失败")
		return
	}

	now := time.Now()
	onlineWindow := now.Add(-2 * time.Minute).Unix()

	type pubMetrics struct {
		CPUPercent float64 `json:"cpu_percent"`
		MemUsed    int64   `json:"mem_used"`
		MemTotal   int64   `json:"mem_total"`
		DiskUsed   int64   `json:"disk_used"`
		DiskTotal  int64   `json:"disk_total"`
		NetUp      int64   `json:"net_up"`
		NetDown    int64   `json:"net_down"`
		Load1      float64 `json:"load1"`
		Load5      float64 `json:"load5"`
		Load15     float64 `json:"load15"`
		Uptime     int64   `json:"uptime"`
	}

	type pubServer struct {
		Name     string      `json:"name"`
		Status   string      `json:"status"`
		Location string      `json:"location"`
		Metrics  *pubMetrics `json:"metrics"`
		LastSeen int64       `json:"last_seen"`
	}

	var out []pubServer
	for _, sv := range servers {
		if !sv.ProbeEnabled {
			continue
		}
		status := "offline"
		if sv.LastSeen >= onlineWindow {
			status = "online"
		}
		var pm *pubMetrics
		if m, _ := a.st.GetLatestMetrics(sv.ID); m != nil {
			pm = &pubMetrics{
				CPUPercent: m.CPUPercent,
				MemUsed:    m.MemUsed,
				MemTotal:   m.MemTotal,
				DiskUsed:   m.DiskUsed,
				DiskTotal:  m.DiskTotal,
				NetUp:      m.NetTx,
				NetDown:    m.NetRx,
				Load1:      m.Load1,
				Load5:      m.Load5,
				Load15:     m.Load15,
				Uptime:     m.Uptime,
			}
		}
		out = append(out, pubServer{
			Name:     sv.Name,
			Status:   status,
			Location: sv.Location,
			Metrics:  pm,
			LastSeen: sv.LastSeen,
		})
	}
	if out == nil {
		out = []pubServer{}
	}
	ok(w, J{"servers": out})
}

// ---- Background tasks ----

// StartMonitorTasks starts the periodic probe alert checker and metrics pruner.
func (a *API) StartMonitorTasks(ctx context.Context) {
	go func() {
		t := time.NewTicker(1 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := a.st.CheckProbeAlerts(); err != nil {
					log.Printf("probe alert check: %v", err)
				}
				if err := a.st.PruneMetrics(30); err != nil {
					log.Printf("metrics prune: %v", err)
				}
			}
		}
	}()
}
