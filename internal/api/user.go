package api

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/auth"
	"qingzhou/internal/idgen"
	"qingzhou/internal/store"
	"qingzhou/internal/subconv"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)

// ---- Registration ----

func (a *API) handleRegister(w http.ResponseWriter, r *http.Request) {
	mode := a.registerMode()
	if mode == "closed" {
		fail(w, http.StatusForbidden, "注册当前未开放")
		return
	}
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if !usernameRe.MatchString(req.Username) {
		fail(w, http.StatusBadRequest, "用户名需为 3-32 位字母、数字或下划线")
		return
	}
	if len(req.Password) < 6 {
		fail(w, http.StatusBadRequest, "密码至少 6 位")
		return
	}
	verifyRequired, _ := a.st.GetSettingBool("email_verify_required")
	if verifyRequired && req.Email == "" {
		fail(w, http.StatusBadRequest, "需要邮箱以完成验证")
		return
	}
	if u, _ := a.st.UserByUsername(req.Username); u != nil {
		fail(w, http.StatusConflict, "用户名已被占用")
		return
	}
	if req.Email != "" {
		if u, _ := a.st.UserByEmail(req.Email); u != nil {
			fail(w, http.StatusConflict, "邮箱已被注册")
			return
		}
	}
	if mode == "code" {
		// Pre-check only — do NOT consume the slot yet. Consuming here (before the
		// account is durably created) means any later failure — a username lost to a
		// concurrent signup, a verify-token write error — would burn a single-use
		// code forever. The atomic decrement happens in finalizeRegCode below, once
		// the user exists.
		if _, ok2 := a.st.RegCodeRedeemable(req.Code); !ok2 {
			fail(w, http.StatusBadRequest, "注册码无效或已用完")
			return
		}
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	traffic, _ := a.st.GetSettingInt64("default_traffic", 10<<30)
	deviceLimit, _ := a.st.GetSettingInt64("default_device_limit", 3)
	expiryDays, _ := a.st.GetSettingInt64("default_expiry_days", 30)
	bonus, _ := a.st.GetSettingInt64("signup_bonus_points", 0)
	subToken, err := idgen.RandToken(24)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	expiryAt := int64(0)
	if expiryDays > 0 {
		expiryAt = time.Now().Unix() + expiryDays*86400
	}

	id, err := a.st.CreateUser(store.NewUser{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: hash,
		Points:       bonus,
		SubToken:     subToken,
		TrafficLimit: traffic,
		DeviceLimit:  deviceLimit,
		ExpiryAt:     expiryAt,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建用户失败")
		return
	}
	u, err := a.st.UserByID(id)
	if err != nil || u == nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	// finalizeRegCode atomically consumes the code and records the use — but only
	// now that the account is durably created, so nothing before this point can burn
	// a slot. Returns false if the code's last slot was taken in the race between the
	// pre-check and here (the caller then rolls back the half-created user).
	finalizeRegCode := func() bool {
		if mode != "code" {
			return true
		}
		cid, ok2 := a.st.ConsumeRegCode(req.Code)
		if !ok2 {
			return false
		}
		_ = a.st.RecordRegCodeUse(cid, u.ID, u.Username, u.Email.String)
		// Grant the code's user groups, so a code handed to a specific crowd unlocks
		// that crowd's packages. Membership alone gives nothing until they buy.
		if gids, gerr := a.st.RegCodeUserGroupIDs(cid); gerr == nil {
			_ = a.st.AddUserGroups(u.ID, gids)
		}
		return true
	}

	// If email verification is required, defer provisioning until verified.
	if verifyRequired {
		token, _ := idgen.RandToken(24)
		if err := a.st.CreateEmailToken(id, token, "verify", 24*time.Hour); err != nil {
			_ = a.st.DeleteUser(id)
			fail(w, http.StatusInternalServerError, "创建验证令牌失败")
			return
		}
		if !finalizeRegCode() {
			_ = a.st.DeleteUser(id)
			fail(w, http.StatusBadRequest, "注册码已被用完，请重试")
			return
		}
		link := a.publicBase(r) + "/api/auth/verify?token=" + token
		a.deliver(req.Email, "验证你的邮箱 - 轻舟", verifyEmailHTML(link), link)
		ok(w, J{"need_verify": true, "message": "注册成功，请查收验证邮件后激活账号"})
		return
	}

	// Otherwise provision immediately.
	if err := a.provisionClient(u); err != nil {
		_ = a.st.DeleteUser(id) // a user without a working node is useless
		log.Printf("register: provision failed for %q: %v", req.Username, err)
		fail(w, http.StatusBadGateway, "开通节点失败，请稍后重试")
		return
	}
	if !finalizeRegCode() {
		_ = a.st.DeleteUser(id)
		fail(w, http.StatusBadRequest, "注册码已被用完，请重试")
		return
	}

	u, _ = a.st.UserByID(id)
	tok, _ := a.issueLogin(w, r, u)
	ok(w, J{"token": tok, "user": userView(u)})
}

// provisionClient creates the user's proxy identity: it mints and stores
// credentials, seeds the user's traffic-pool bucket, then rebuilds the sing-box
// config so the client goes live.
func (a *API) provisionClient(u *store.User) error {
	if a.sbctl == nil {
		return errSingboxNotEnabled
	}
	name := userClientName(u.Username)
	cr, err := idgen.NewCredentials()
	if err != nil {
		return err
	}
	if err := a.st.SetUserClient(u.ID, 0, name, cr.UUID, cr.Password); err != nil {
		return err
	}
	// The user's primary identity is their traffic-pool bucket (covers free
	// nodes + any traffic packages); plan purchases add their own buckets.
	if err := a.st.EnsurePoolBucket(u.ID, name, cr.UUID, cr.Password); err != nil {
		return err
	}
	return a.sbRebuild()
}

func userClientName(username string) string { return "qz_" + username }

// syncEntitlement is the post-entitlement-change hook passed to
// Purchase/Assign/Refund. The sing-box config is regenerated from the DB, so
// there is nothing to push here — and it must NOT rebuild inside the (still-open)
// purchase transaction; handlers trigger sbRebuild() after commit and the
// controller's periodic loop backstops.
func (a *API) syncEntitlement(_ *store.User, _ bool) error { return nil }

// ---- User dashboard / subscription ----

func (a *API) handleDashboard(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	used := u.UsedUp + u.UsedDown
	remaining := int64(0)
	if u.TrafficLimit > used {
		remaining = u.TrafficLimit - used
	}
	plan := ""
	if u.CurrentPlanID.Valid {
		if p, _ := a.st.GetPackage(u.CurrentPlanID.Int64); p != nil {
			plan = p.Name
		}
	}
	// Per-plan breakdown (each bucket metered independently). The traffic block
	// above is the legacy roll-up kept for back-compat; the dashboard should
	// surface per-plan remaining/expiry from here so multiple plans don't merge
	// into one number.
	buckets, _ := a.st.ListBuckets(u.ID)
	ok(w, J{
		"username": u.Username,
		"email":    nsOr(u.Email),
		"points":   u.Points,
		"status":   u.Status,
		"traffic": J{
			"used":      used,
			"total":     u.TrafficLimit, // 0 = unlimited
			"remaining": remaining,
		},
		"plans":            buildPlanViews(buckets),
		"expiry_at":        u.ExpiryAt,
		"current_plan":     plan,
		"subscription_url": a.subURL(r, u),
	})
}

// POST /api/user/reset-sub — rotate the subscription token (old link dies).
func (a *API) handleResetSub(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	token, err := idgen.RandToken(24)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if err := a.st.SetSubToken(u.ID, token); err != nil {
		fail(w, http.StatusInternalServerError, "重置失败")
		return
	}
	a.invalidateLinks(u.ID)
	u, _ = a.st.UserByID(u.ID)
	ok(w, J{"url": a.subURL(r, u)})
}

func (a *API) handleSubscription(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	base := a.subURL(r, u)
	ok(w, J{
		"url": base,
		"formats": J{
			"default": base,
			"clash":   base + "?format=clash",
			"singbox": base + "?format=singbox",
			"surge":   base + "?format=surge",
		},
	})
}

// handleUserProxies returns the user's entitled mixed (HTTP/SOCKS5) inbounds as
// copyable proxy credentials. These plain-proxy nodes are excluded from the
// Clash/sing-box subscription on purpose, so this is how the user retrieves the
// address/port/username/password to paste into tools like 1Panel or Docker.
func (a *API) handleUserProxies(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	proxies := a.st.BuildUserProxies(u, a.nodeHost())
	if proxies == nil {
		proxies = []store.UserProxy{}
	}
	ok(w, proxies)
}

// handleUpdateUserProxy sets the caller's custom mixed-proxy credential on one of
// their buckets: a proxy-only account (username/password, unrelated to login)
// with an optional expiry. Rotatable anytime — the config is rebuilt so sing-box
// picks up the new credential immediately.
func (a *API) handleUpdateUserProxy(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	bucketID := int64(atoi(chi.URLParam(r, "bucket")))
	if bucketID <= 0 {
		fail(w, http.StatusBadRequest, "无效的套餐 id")
		return
	}
	var req struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		ExpiresAt int64  `json:"expires_at"` // 0 = permanent
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.st.SetBucketProxyCred(bucketID, u.ID, strings.TrimSpace(req.Username), req.Password, req.ExpiresAt); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	a.sbRebuildLog()
	ok(w, nil)
}

// handleUserPlans returns the user's subscription plans plus the traffic pool,
// each metered independently — this is what stops multiple plans from merging
// their traffic and expiry into one pool.
func (a *API) handleUserPlans(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	buckets, _ := a.st.ListBuckets(u.ID)
	ok(w, buildPlanViews(buckets))
}

type planView struct {
	ID           int64  `json:"id"`
	Kind         string `json:"kind"`
	PackageID    int64  `json:"package_id"`
	Name         string `json:"name"`
	TrafficLimit int64  `json:"traffic_limit"`
	Used         int64  `json:"used"`
	Remaining    int64  `json:"remaining"` // -1 = unlimited
	ExpiryAt     int64  `json:"expiry_at"`
	Status       string `json:"status"` // active | expired | exhausted
}

// buildPlanViews shapes a user's buckets for the UI: each plan + the pool (if it
// has any balance), with remaining traffic and a derived status.
func buildPlanViews(buckets []*store.Bucket) []planView {
	now := time.Now().Unix()
	out := []planView{}
	for _, b := range buckets {
		if b.Kind == "pool" && b.TrafficLimit == 0 {
			continue // empty/inert pool — nothing to show
		}
		pv := planView{ID: b.ID, Kind: b.Kind, PackageID: b.PackageID, Name: b.Name, TrafficLimit: b.TrafficLimit,
			Used: b.Used(), ExpiryAt: b.ExpiryAt, Remaining: -1}
		if b.TrafficLimit > 0 {
			if rem := b.TrafficLimit - b.Used(); rem > 0 {
				pv.Remaining = rem
			} else {
				pv.Remaining = 0
			}
		}
		switch {
		case !b.NotExpired(now):
			pv.Status = "expired"
		case !b.HasQuota():
			pv.Status = "exhausted"
		default:
			pv.Status = "active"
		}
		out = append(out, pv)
	}
	return out
}

// ---- Public subscription endpoint ----

// handleSub aggregates the user's accessible nodes (self-built from sing-box +
// external) filtered by their plan/free groups, and renders the requested
// format (base64 / clash / singbox) with the anti-leak template injected.
func (a *API) handleSub(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	u, err := a.st.UserBySubToken(token)
	if err != nil || u == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Banned users get nothing — for external nodes the served link is the real
	// upstream credential, which the panel cannot meter or cut off after the fact.
	if u.Status == "banned" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Over-quota / expired users are served an empty node list (still a valid,
	// well-formed config) rather than working links. sing-box self-built access is
	// enforced separately, but external-node links must be withheld here too.
	now := time.Now().Unix()
	serviceable := (u.ExpiryAt == 0 || u.ExpiryAt > now) &&
		(u.TrafficLimit == 0 || u.UsedUp+u.UsedDown < u.TrafficLimit)

	// Build the link list + a link→group map (drives the per-group auto-select
	// groups), honoring the user's per-node blocklist.
	disabled, _ := a.st.DisabledNodeKeys(u.ID)
	var links []string
	groups := map[string]string{}
	if serviceable {
		for _, e := range a.collectEntries(u) {
			if subconv.NodeDisabled(disabled, e.Link) {
				continue
			}
			links = append(links, e.Link)
			if e.GroupName != "" {
				groups[e.Link] = e.GroupName
			}
		}
	}

	clashTpl, _ := a.st.GetSetting("sub_clash_template")
	singboxTpl, _ := a.st.GetSetting("sub_singbox_template")
	// Explicit ?format= wins; otherwise auto-detect from the client User-Agent
	// so Clash/sing-box/Surge each get a native config out of the box.
	format := r.URL.Query().Get("format")
	if format == "" {
		format = subconv.FormatForUA(r.Header.Get("User-Agent"))
	}
	subURL := a.publicBase(r) + r.URL.Path
	body, ctype, err := subconv.Render(format, links, groups, clashTpl, singboxTpl, subURL)
	if err != nil {
		http.Error(w, "render error", http.StatusBadGateway)
		return
	}

	w.Header().Set("Subscription-Userinfo",
		"upload="+itoa(u.UsedUp)+"; download="+itoa(u.UsedDown)+"; total="+itoa(u.TrafficLimit)+"; expire="+itoa(u.ExpiryAt))
	w.Header().Set("Profile-Update-Interval", "12")
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write([]byte(body))
}

type linkCacheEntry struct {
	entries []nodeEntry
	exp     int64
}

// userHasNodeAccess reports whether the user is entitled to any self-built node,
// i.e. whether their sing-box client should be enabled. No plan + no free group = no
// access — unless grouping isn't set up at all (zero-config convenience).
// This is the real enforcement: subscription filtering only changes what links
// are shown; sing-box enable/inbound membership is what actually grants access.
func (a *API) userHasNodeAccess(u *store.User) bool {
	if gids, _ := a.st.AccessibleGroupIDs(u); len(gids) > 0 {
		return true
	}
	if n, _ := a.st.GroupCount(); n == 0 {
		return true
	}
	return false
}

// collectEntries returns the user's nodes (link + group), cached ~30s to avoid
// recomputing on every client poll.
func (a *API) collectEntries(u *store.User) []nodeEntry {
	a.linkMu.Lock()
	if e, ok := a.linkCache[u.ID]; ok && time.Now().Unix() < e.exp {
		a.linkMu.Unlock()
		return e.entries
	}
	a.linkMu.Unlock()

	entries := a.computeNodeEntries(u)

	now := time.Now().Unix()
	a.linkMu.Lock()
	// Evict expired entries so the map doesn't grow unbounded with users who
	// fetched once (e.g. deleted accounts). Cheap: bounded by the 30s TTL.
	for id, e := range a.linkCache {
		if now >= e.exp {
			delete(a.linkCache, id)
		}
	}
	a.linkCache[u.ID] = linkCacheEntry{entries: entries, exp: now + 30}
	a.linkMu.Unlock()
	return entries
}

// collectLinks returns just the user's share links (subscription/ping order).
func (a *API) collectLinks(u *store.User) []string {
	es := a.collectEntries(u)
	links := make([]string, len(es))
	for i, e := range es {
		links[i] = e.Link
	}
	return links
}

func (a *API) invalidateLinks(userID int64) {
	a.linkMu.Lock()
	delete(a.linkCache, userID)
	a.linkMu.Unlock()
}

// nodeEntry is a share link plus the accessible group it was served from.
type nodeEntry struct {
	Link      string
	GroupID   int64
	GroupName string
}

// computeNodeEntries builds the user's nodes with group attribution: external
// nodes in their accessible groups (raw), plus self-built node links filtered to
// the inbound tags in those groups. If no groups are configured at all, falls
// back to all self-built nodes (zero-config). Deduped by link.
func (a *API) computeNodeEntries(u *store.User) []nodeEntry {
	var out []nodeEntry
	seen := map[string]bool{}
	add := func(l string, gid int64, gname string) {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, nodeEntry{Link: l, GroupID: gid, GroupName: gname})
		}
	}

	groupIDs, _ := a.st.AccessibleGroupIDs(u)

	if len(groupIDs) == 0 {
		// User has no accessible group (no plan, no free group). Only fall back
		// to all self-built nodes when grouping isn't set up at all (zero-config
		// convenience). Once any group exists, "unassigned" means "no nodes".
		if n, _ := a.st.GroupCount(); n == 0 {
			for _, l := range a.selfBuiltLinks(u) {
				add(l.Link, 0, "")
			}
		}
		return out
	}

	gname := map[int64]string{}
	if gs, err := a.st.ListGroups(); err == nil {
		for _, g := range gs {
			gname[g.ID] = g.Name
		}
	}

	nodes, _ := a.st.NodesInGroupsTagged(groupIDs)
	tagGroup := map[string]int64{} // self-built inbound tag → representative group
	for _, n := range nodes {
		switch n.Type {
		case "external":
			add(n.ShareLink, n.GroupID, gname[n.GroupID])
		case "self_built":
			if n.InboundTag != "" {
				tagGroup[n.InboundTag] = n.GroupID
			}
		}
	}
	if len(tagGroup) > 0 {
		for _, l := range a.selfBuiltLinks(u) {
			if gid, ok := tagGroup[l.Tag]; ok {
				add(l.Link, gid, gname[gid])
			}
		}
	}
	return out
}

// nodeHost resolves the address advertised to clients for self-built nodes: the
// node_host_override setting, else the first enabled server's host.
func (a *API) nodeHost() string {
	host, _ := a.st.GetSetting("node_host_override")
	if host == "" {
		// Auto-detect: fall back to the first enabled server's host.
		if servers, err := a.st.ListServers(); err == nil {
			for _, sv := range servers {
				if sv.Enabled && sv.Host != "" {
					host = sv.Host
					break
				}
			}
		}
	}
	return host
}

func (a *API) selfBuiltLinks(u *store.User) []store.SelfBuiltLink {
	// 轻舟 manages its own sing-box inbounds; build the links from our own data.
	return a.st.BuildSelfBuiltLinks(u, a.nodeHost())
}

// ---- Admin: delete user ----

func (a *API) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的用户 id")
		return
	}
	u, err := a.st.UserByID(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if u == nil {
		fail(w, http.StatusNotFound, "用户不存在")
		return
	}
	if u.Role == "admin" {
		fail(w, http.StatusForbidden, "不能删除管理员账号")
		return
	}
	if err := a.st.DeleteUser(id); err != nil {
		fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	a.invalidateLinks(id)
	a.sbRebuildLog()
	ok(w, nil)
}

// ---- helpers ----

func (a *API) currentUser(r *http.Request) *store.User {
	uid, _ := r.Context().Value(ctxUserID).(int64)
	u, _ := a.st.UserByID(uid)
	return u
}

func (a *API) subURL(r *http.Request, u *store.User) string {
	if !u.SubToken.Valid {
		return ""
	}
	return a.publicBase(r) + "/sub/" + u.SubToken.String
}
