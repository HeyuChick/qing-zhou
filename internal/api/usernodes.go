package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"qingzhou/internal/store"
	"qingzhou/internal/subconv"
)

func (a *API) userProxies(u *store.User) []*subconv.Proxy {
	return subconv.ParseLinks(a.collectLinks(u))
}

// GET /api/user/nodes — the nodes in the user's current subscription, each with
// a stable key, group attribution, and the user's own enable/disable state.
func (a *API) handleUserNodes(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	entries := a.computeNodeEntries(u)
	disabled, _ := a.st.DisabledNodeKeys(u.ID)
	out := make([]J, 0, len(entries))
	for _, e := range entries {
		p, err := subconv.ParseLink(e.Link)
		if err != nil || p == nil {
			continue
		}
		k := subconv.NodeKey(e.Link)
		out = append(out, J{"name": p.Name, "protocol": p.Protocol, "server": p.Server, "port": p.Port,
			"key": k, "disabled": disabled[k], "group": e.GroupName})
	}
	ok(w, out)
}

// POST /api/user/nodes/toggle {key, disabled} — disable/enable one node for self.
func (a *API) handleUserToggleNode(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	var req struct {
		Key      string `json:"key"`
		Disabled bool   `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		fail(w, http.StatusBadRequest, "参数错误")
		return
	}
	if err := a.st.SetNodeDisabled(u.ID, req.Key, req.Disabled); err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	ok(w, nil)
}

// POST /api/user/nodes/disable-all — disable every node in the user's current
// subscription (for self only).
func (a *API) handleUserDisableAllNodes(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	ps := a.userProxies(u)
	keys := make([]string, 0, len(ps))
	for _, p := range ps {
		keys = append(keys, subconv.NodeKey(p.Raw))
	}
	if err := a.st.DisableNodeKeys(u.ID, keys); err != nil {
		fail(w, http.StatusInternalServerError, "操作失败")
		return
	}
	ok(w, J{"disabled": len(keys)})
}

// POST /api/user/nodes/bulk {enable:[keys], disable:[keys]} — enable/disable many
// nodes at once (used by the latency-range condition; keys not listed untouched).
func (a *API) handleUserBulkNodes(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	var req struct {
		Enable  []string `json:"enable"`
		Disable []string `json:"disable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "参数错误")
		return
	}
	if err := a.st.ApplyNodePrefs(u.ID, req.Disable, req.Enable); err != nil {
		fail(w, http.StatusInternalServerError, "操作失败")
		return
	}
	ok(w, J{"enabled": len(req.Enable), "disabled": len(req.Disable)})
}

// POST /api/user/nodes/enable-all — clear the user's blocklist.
func (a *API) handleUserEnableAllNodes(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	if err := a.st.EnableAllNodes(u.ID); err != nil {
		fail(w, http.StatusInternalServerError, "操作失败")
		return
	}
	ok(w, nil)
}

// udpProto: protocols that ride UDP/QUIC — a TCP probe is meaningless for them.
var udpProto = map[string]bool{"tuic": true, "hysteria": true, "hysteria2": true}

type pingResult struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Latency  int64  `json:"latency"`
	OK       bool   `json:"ok"`
	UDP      bool   `json:"udp"`
	Key      string `json:"key"`
	Disabled bool   `json:"disabled"`
	Group    string `json:"group"`
}

// GET /api/user/nodes/ping — server-side TCP latency probe (reference only;
// real throughput must be tested in the client app).
func (a *API) handleUserNodesPing(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	entries := a.computeNodeEntries(u)
	disabled, _ := a.st.DisabledNodeKeys(u.ID)
	out := make([]pingResult, len(entries))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64) // cap concurrent dials (lists can be hundreds of nodes)
	for i := range entries {
		p, err := subconv.ParseLink(entries[i].Link)
		if err != nil || p == nil {
			continue
		}
		k := subconv.NodeKey(entries[i].Link)
		out[i] = pingResult{Name: p.Name, Protocol: p.Protocol, Server: p.Server, Port: p.Port,
			Key: k, Disabled: disabled[k], Group: entries[i].GroupName}
		if udpProto[p.Protocol] {
			out[i].UDP = true
			continue
		}
		if p.Server == "" || p.Port == 0 {
			continue
		}
		wg.Add(1)
		go func(idx int, server string, port int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			start := time.Now()
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(server, strconv.Itoa(port)), 2*time.Second)
			if err == nil {
				_ = conn.Close()
				out[idx].OK = true
				out[idx].Latency = time.Since(start).Milliseconds()
			}
		}(i, p.Server, p.Port)
	}
	wg.Wait()
	ok(w, out)
}
