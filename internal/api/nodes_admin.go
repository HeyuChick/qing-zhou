package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
	"qingzhou/internal/subconv"
)

// ---- nodes ----

func (a *API) handleAdminListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.st.ListNodes()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取节点失败")
		return
	}
	ok(w, nodes)
}

// handleAdminReorderNodes persists a new display order for the node page and the
// subscriptions it generates. Body: {"ids":[...]} — node ids in the desired
// global order. Only sort_order is rewritten. No sing-box rebuild is needed
// (server config keys off inbounds, not node order), but subscriptions render in
// this order, so refresh the link cache by touching nothing else.
func (a *API) handleAdminReorderNodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.st.ReorderNodes(req.IDs); err != nil {
		fail(w, http.StatusInternalServerError, "保存排序失败")
		return
	}
	ok(w, J{"count": len(req.IDs)})
}

func (a *API) handleAdminCreateNode(w http.ResponseWriter, r *http.Request) {
	var n store.Node
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if n.Type != "self_built" && n.Type != "external" {
		fail(w, http.StatusBadRequest, "节点类型必须为 self_built / external")
		return
	}
	if n.Type == "external" && n.ShareLink != "" {
		if p, err := subconv.ParseLink(n.ShareLink); err == nil {
			if n.Protocol == "" {
				n.Protocol = p.Protocol
			}
			if n.Name == "" {
				n.Name = p.Name
			}
		}
	}
	id, err := a.st.CreateNode(n)
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建节点失败")
		return
	}
	created, _ := a.st.GetNode(id)
	a.sbRebuildLog()
	ok(w, created)
}

func (a *API) handleAdminUpdateNode(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	// Decode onto the stored row: UpdateNode writes every column, but the edit
	// form only posts a few. Into a zero value, saving a node — even just
	// toggling 启用 — blanked its share_link and protocol, dropping it out of the
	// generated sing-box config and every user's subscription.
	n, err := a.st.GetNode(int64(id))
	if err != nil || n == nil {
		fail(w, http.StatusNotFound, "节点不存在")
		return
	}
	if err := json.NewDecoder(r.Body).Decode(n); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	n.ID = int64(id)
	if err := a.st.UpdateNode(*n); err != nil {
		fail(w, http.StatusInternalServerError, "更新节点失败")
		return
	}
	a.sbRebuildLog()
	ok(w, nil)
}

func (a *API) handleAdminDeleteNode(w http.ResponseWriter, r *http.Request) {
	if err := a.st.DeleteNode(atoi(chi.URLParam(r, "id"))); err != nil {
		fail(w, http.StatusInternalServerError, "删除节点失败")
		return
	}
	a.sbRebuildLog()
	ok(w, nil)
}

// POST /api/admin/nodes/import {links, group_ids}
func (a *API) handleAdminImportNodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Links    string  `json:"links"`
		GroupIDs []int64 `json:"group_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	proxies := subconv.ParseList(req.Links)
	n := 0
	for _, p := range proxies {
		id, err := a.st.CreateNode(store.Node{
			Type: "external", Name: p.Name, Protocol: p.Protocol,
			ShareLink: p.Raw, Enabled: true, GroupIDs: req.GroupIDs,
		})
		if err == nil && id > 0 {
			n++
		}
	}
	ok(w, J{"imported": n})
}

// ---- groups ----

func (a *API) handleAdminListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := a.st.ListGroups()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取分组失败")
		return
	}
	// Attach node_count: count how many nodes reference each group.
	counts := map[int64]int64{}
	if nodes, err := a.st.ListNodes(); err == nil {
		for _, n := range nodes {
			for _, gid := range n.GroupIDs {
				counts[gid]++
			}
		}
	}
	out := make([]J, 0, len(groups))
	for _, g := range groups {
		out = append(out, J{
			"id":          g.ID,
			"name":        g.Name,
			"description": g.Description,
			"sort_order":  g.SortOrder,
			"created_at":  g.CreatedAt,
			"node_count":  counts[g.ID],
		})
	}
	ok(w, out)
}

func (a *API) handleAdminCreateGroup(w http.ResponseWriter, r *http.Request) {
	var g store.NodeGroup
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil || g.Name == "" {
		fail(w, http.StatusBadRequest, "分组名称不能为空")
		return
	}
	id, err := a.st.CreateGroup(g)
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建分组失败")
		return
	}
	ok(w, J{"id": id})
}

func (a *API) handleAdminUpdateGroup(w http.ResponseWriter, r *http.Request) {
	var g store.NodeGroup
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	g.ID = atoi(chi.URLParam(r, "id"))
	if err := a.st.UpdateGroup(g); err != nil {
		fail(w, http.StatusInternalServerError, "更新分组失败")
		return
	}
	ok(w, nil)
}

func (a *API) handleAdminDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := a.st.DeleteGroup(atoi(chi.URLParam(r, "id"))); err != nil {
		fail(w, http.StatusInternalServerError, "删除分组失败")
		return
	}
	ok(w, nil)
}

// handleAdminInbounds lists 轻舟's own sing-box inbounds (tag/type) so admins can
// bind self-built nodes to them.
func (a *API) handleAdminInbounds(w http.ResponseWriter, r *http.Request) {
	list, err := a.st.ListSbInbounds()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取入站失败: "+err.Error())
		return
	}
	ok(w, list)
}

// ---- node sources (机场订阅) ----

func (a *API) handleAdminListSources(w http.ResponseWriter, r *http.Request) {
	srcs, err := a.st.ListSources()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取订阅源失败")
		return
	}
	ok(w, srcs)
}

func (a *API) handleAdminCreateSource(w http.ResponseWriter, r *http.Request) {
	var s store.NodeSource
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil || s.URL == "" {
		fail(w, http.StatusBadRequest, "订阅地址不能为空")
		return
	}
	if s.Type == "" {
		s.Type = "base64"
	}
	id, err := a.st.CreateSource(s)
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建订阅源失败")
		return
	}
	ok(w, J{"id": id})
}

func (a *API) handleAdminUpdateSource(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	// Decode onto the stored row — see handleAdminUpdateNode.
	src, err := a.st.GetSource(int64(id))
	if err != nil || src == nil {
		fail(w, http.StatusNotFound, "订阅源不存在")
		return
	}
	if err := json.NewDecoder(r.Body).Decode(src); err != nil || src.URL == "" {
		fail(w, http.StatusBadRequest, "订阅地址不能为空")
		return
	}
	src.ID = int64(id)
	if src.Type == "" {
		src.Type = "base64"
	}
	if err := a.st.UpdateSource(*src); err != nil {
		fail(w, http.StatusInternalServerError, "更新订阅源失败")
		return
	}
	ok(w, nil)
}

func (a *API) handleAdminDeleteSource(w http.ResponseWriter, r *http.Request) {
	if err := a.st.DeleteSource(atoi(chi.URLParam(r, "id"))); err != nil {
		fail(w, http.StatusInternalServerError, "删除订阅源失败")
		return
	}
	ok(w, nil)
}

// POST /api/admin/node-sources/{id}/fetch {group_ids}
func (a *API) handleAdminFetchSource(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	src, err := a.st.GetSource(id)
	if err != nil || src == nil {
		fail(w, http.StatusNotFound, "订阅源不存在")
		return
	}
	var req struct {
		GroupIDs []int64 `json:"group_ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	// Group binding lives on the source so it survives periodic auto-sync. If
	// the request explicitly carries group_ids, persist them as the new binding;
	// otherwise reuse whatever the source already has.
	groups := src.GroupIDs
	if req.GroupIDs != nil {
		groups = req.GroupIDs
		src.GroupIDs = req.GroupIDs
		_ = a.st.UpdateSource(*src)
	}
	count, ferr := a.fetchSource(src, groups)
	if ferr != "" {
		fail(w, http.StatusBadGateway, "抓取失败: "+ferr)
		return
	}
	ok(w, J{"imported": count})
}

// fetchSource downloads a source URL, parses links, and replaces the source's
// nodes. Returns the imported count and an error string (empty on success).
func (a *API) fetchSource(src *store.NodeSource, groupIDs []int64) (int, string) {
	if msg := validFetchURL(src.URL); msg != "" {
		_ = a.st.ReplaceSourceNodes(src.ID, nil, groupIDs, msg)
		return 0, msg
	}
	// Verify TLS and block SSRF to internal addresses (metadata/LAN/localhost).
	client := safeFetchClient()
	resp, err := client.Get(src.URL)
	if err != nil {
		_ = a.st.ReplaceSourceNodes(src.ID, nil, groupIDs, err.Error())
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	proxies := subconv.ParseList(string(body))
	nodes := make([]store.Node, 0, len(proxies))
	for _, p := range proxies {
		nodes = append(nodes, store.Node{Name: p.Name, Protocol: p.Protocol, ShareLink: p.Raw})
	}
	if err := a.st.ReplaceSourceNodes(src.ID, nodes, groupIDs, ""); err != nil {
		return 0, err.Error()
	}
	return len(nodes), ""
}

// StartSourceSync periodically refreshes enabled node sources.
func (a *API) StartSourceSync(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				srcs, err := a.st.ListSources()
				if err != nil {
					continue
				}
				for _, s := range srcs {
					if !s.Enabled {
						continue
					}
					if _, ferr := a.fetchSource(s, s.GroupIDs); ferr != "" {
						log.Printf("source sync %q: %s", s.Name, ferr)
					}
				}
			}
		}
	}()
}
