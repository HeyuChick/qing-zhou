package api

import (
	"encoding/json"
	"errors"
	"fmt"
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
	// The user's primary identity is their traffic-pool bucket (paid traffic
	// packages); plan purchases add their own buckets.
	if err := a.st.EnsurePoolBucket(u.ID, name, cr.UUID, cr.Password); err != nil {
		return err
	}
	// Free-group access rides its own bucket so its traffic is metered separately
	// from the paid pool rather than debited against it.
	if err := a.st.EnsureFreeBucket(u.ID, u.Username); err != nil {
		return err
	}
	// The signup grant has to live in a bucket too — enforcement never reads the
	// users.* columns registration used to write it to.
	traffic, _ := a.st.GetSettingInt64("default_traffic", 10<<30)
	expiryDays, _ := a.st.GetSettingInt64("default_expiry_days", 30)
	expiry := int64(0)
	if expiryDays > 0 {
		expiry = time.Now().Unix() + expiryDays*86400
	}
	if err := a.st.EnsureWelcomeBucket(u.ID, u.Username, traffic, expiry); err != nil {
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
	buckets, _ := a.st.ListBuckets(u.ID)
	pkgNames, _ := a.st.PackageNames()
	tr := dashboardTraffic(buckets)
	ok(w, J{
		"username": u.Username,
		"email":    nsOr(u.Email),
		"points":   u.Points,
		"status":   u.Status,
		"traffic": J{
			"used":      tr.Used,
			"total":     tr.Total, // 0 = no metered quota at all; read Unlimited to tell which
			"remaining": tr.Remaining,
			// Unlimited says "0 total" means 不限, not 没额度 — the two render
			// completely differently and the number alone cannot distinguish them.
			"unlimited":      tr.Unlimited,
			"unmetered_used": tr.UnmeteredUsed,
		},
		// Plans stay per-bucket so the UI can show every active/queued份 with its
		// own quota and expiry; there is deliberately no single "current plan" —
		// several can be live at once and a queued repeat purchase means one of
		// them isn't actually in use yet.
		"plans":     buildPlanViews(buckets, pkgNames),
		"expiry_at": u.ExpiryAt,
	})
}

// POST /api/user/reset-sub — serve the subscription at a new address.
//
// Panel-only and instant: the token is not part of any node's config, so no
// server is touched and no other user's connection is disturbed. This is the
// answer to a leaked *address*, and it is deliberately the cheap, unrestricted
// action.
//
// It does not revoke anything. The node links the old address already served
// authenticate with the bucket's uuid/password and keep working — cutting those
// off is handleResetNodeCreds, which has to reach every node and is gated
// accordingly. The UI must not promise more than this does.
func (a *API) handleResetSub(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	// Cheap is not the same as free: every swap revokes the previous address, so
	// a runaway caller can keep a user permanently unable to finish an import.
	if a.subRL != nil && !a.subRL.allow(fmt.Sprintf("s%d", u.ID)) {
		fail(w, http.StatusTooManyRequests, "更换过于频繁，请稍后再试")
		return
	}
	token, err := idgen.RandToken(24)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if err := a.st.RotateSubToken(u.ID, token); err != nil {
		fail(w, http.StatusInternalServerError, "更换失败")
		return
	}
	a.invalidateLinks(u.ID)
	if fresh, err := a.st.UserByID(u.ID); err == nil && fresh != nil {
		u = fresh
	}
	ok(w, J{"url": a.subURL(r, u)})
}

// credsResetCooldown is how long a user must wait between node-credential
// rotations. Applying one restarts sing-box on every server carrying their
// nodes, which drops every *other* user's live connections too, so this is a
// deliberately expensive action to take.
const credsResetCooldown = 30 * 24 * time.Hour

// credsResetEnabled reports whether users may rotate their own node credentials.
// Off unless an admin turns it on: the action is disruptive panel-wide, so it
// ships disabled and the UI points users at the admin instead.
func (a *API) credsResetEnabled() bool {
	v, _ := a.st.GetSetting("node_creds_reset_enabled")
	return v == "true"
}

// POST /api/user/reset-node-creds — rotate the credentials the user's node links
// authenticate with, revoking every link a leaked subscription already handed
// out (issue #6).
//
// Three gates, because this is the expensive half:
//   - a kill switch, default off — the endpoint is not merely hidden in the UI;
//     a disabled button is not an access control.
//   - a 30-day cooldown per user, enforced inside the rotation's transaction so
//     two concurrent requests cannot both slip through.
//   - no explicit rebuild. The controller's periodic pass (every minute by
//     default) picks the change up and batches it with whatever else changed,
//     so a rotation adds no extra sing-box restart of its own. The cost is that
//     the new credentials take up to one interval to work.
//
// When the switch is off, handleAdminResetNodeCreds is the way through — which
// is what "请联系管理员" in the error and in the UI refers to.
func (a *API) handleResetNodeCreds(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	if !a.credsResetEnabled() {
		fail(w, http.StatusForbidden, "该功能暂时禁用，有需要请联系管理员")
		return
	}
	cutoff := time.Now().Add(-credsResetCooldown).Unix()
	if err := a.st.RotateNodeCredentials(u.ID, cutoff); err != nil {
		if errors.Is(err, store.ErrCredsResetCooldown) {
			fail(w, http.StatusTooManyRequests, a.credsCooldownMsg(u.ID))
			return
		}
		fail(w, http.StatusInternalServerError, "重置失败")
		return
	}
	// Rotating credentials is a security-relevant act on a live account: without
	// a record, "when were this user's credentials last changed, and by whom" is
	// unanswerable after the fact.
	log.Printf("audit: node credentials rotated by user id=%d (self-service)", u.ID)
	a.invalidateLinks(u.ID)
	ok(w, J{"applies_in_seconds": int64(a.sbSyncInterval().Seconds())})
}

// POST /api/admin/users/{id}/reset-node-creds — the operator half of the node
// credential rotation, and the thing 「请联系管理员」 actually refers to.
//
// The user-facing endpoint ships behind a kill switch precisely so that most
// panels route this through an operator, so it would be circular for this path
// to honor that switch. It also skips the 30-day cooldown — an admin doing this
// is responding to a leak on an account that is, by definition, rate-limited.
//
// Unlike the self-service path it pushes immediately rather than riding the
// periodic pass: someone is standing in front of an active leak, and saving one
// sing-box restart is not worth up to a minute of the old credentials still
// working.
func (a *API) handleAdminResetNodeCreds(w http.ResponseWriter, r *http.Request) {
	uid := atoi(chi.URLParam(r, "id"))
	if uid <= 0 {
		fail(w, http.StatusBadRequest, "无效的用户 id")
		return
	}
	if err := a.st.RotateNodeCredentials(uid, store.NoCredsResetCooldown); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			fail(w, http.StatusNotFound, "用户不存在")
			return
		}
		fail(w, http.StatusInternalServerError, "重置失败")
		return
	}
	operatorID, _ := r.Context().Value(ctxUserID).(int64)
	log.Printf("audit: node credentials rotated for user id=%d by admin id=%d", uid, operatorID)
	a.invalidateLinks(uid)
	a.sbRebuildLog()
	ok(w, J{"user_id": uid})
}

// credsCooldownMsg renders the "come back in N days" refusal, re-reading the
// stamp so the count is right even when the caller lost a race to another
// request rather than tripping the cooldown it already knew about.
func (a *API) credsCooldownMsg(userID int64) string {
	days := 1
	if u, err := a.st.UserByID(userID); err == nil && u != nil {
		if wait := time.Until(time.Unix(u.CredsResetAt, 0).Add(credsResetCooldown)); wait > 0 {
			days = int(wait/(24*time.Hour)) + 1
		}
	}
	return fmt.Sprintf("节点凭据 30 天内只能重置一次，请 %d 天后再试", days)
}

func (a *API) handleSubscription(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	base := a.subURL(r, a.ensureSubToken(u))
	if base == "" {
		// No token and minting failed. Report nothing rather than the bare
		// "?format=clash" fragments that concatenating onto "" produces — those
		// look like links and resolve to the SPA.
		ok(w, J{"url": "", "formats": J{}, "creds_reset_enabled": a.credsResetEnabled()})
		return
	}
	ok(w, J{
		"url": base,
		// Drives the 「重置节点凭据」 button. Served from the same kill switch the
		// endpoint enforces, so the button reflects reality instead of being
		// hardcoded — and the switch has an effect users can see.
		"creds_reset_enabled": a.credsResetEnabled(),
		"formats": J{
			"default": base,
			"clash":   base + "?format=clash",
			"singbox": base + "?format=singbox",
			"surge":   base + "?format=surge",
			// The base64 link list (v2rayN / NekoBox / Shadowrocket / Quantumult).
			// "default" also yields it today, but only by User-Agent fallback — a
			// client that sends a UA containing "clash" silently gets YAML instead.
			// This entry pins the format regardless of UA.
			"base64": base + "?format=base64",
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
	pkgNames, _ := a.st.PackageNames()
	ok(w, buildPlanViews(buckets, pkgNames))
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
	Status       string `json:"status"` // active | queued | expired | exhausted
	// ActivateBy is a queued plan's estimated LATEST activation time (unix): the
	// current head's expiry plus the durations of the queued份 ahead of it. It may
	// activate sooner if the head's traffic runs out first. 0 = unknown (the head
	// has no expiry, so only exhaustion triggers the next). Only set for queued.
	ActivateBy int64 `json:"activate_by,omitempty"`
	// CreatedAt is when this份 was granted, and OrderID the purchase it came from
	// (0 = no order: signup grant, admin grant, admin assignment). The admin panel
	// orders份 of the same package by these and uses OrderID to point at the
	// refund action, which is a different thing from removing the份.
	CreatedAt int64 `json:"created_at"`
	OrderID   int64 `json:"order_id"`
}

// queueActivations estimates, for each queued plan bucket, the LATEST time it
// will activate: from the usable head's expiry, walking the same-package queue in
// id order and adding each份's duration. 0 = unknown (unlimited-duration head →
// only exhaustion advances it). When a package's head has already ended but isn't
// promoted yet (the ~2min ticker gap), the oldest queued份 is treated as due now.
func queueActivations(buckets []*store.Bucket, now int64) map[int64]int64 {
	type pkgQ struct {
		base    int64 // head's expiry (0 = never/unlimited)
		hasHead bool
		queued  []*store.Bucket
	}
	byPkg := map[int64]*pkgQ{}
	for _, b := range buckets {
		if b.Kind != "plan" || b.PackageID <= 0 {
			continue
		}
		q := byPkg[b.PackageID]
		if q == nil {
			q = &pkgQ{}
			byPkg[b.PackageID] = q
		}
		switch {
		case b.Status == "queued":
			q.queued = append(q.queued, b) // ListBuckets is id-ordered → FIFO
		case b.Status == "active" && b.NotExpired(now) && b.HasQuota():
			q.hasHead = true
			if b.ExpiryAt > q.base {
				q.base = b.ExpiryAt
			}
		}
	}
	out := map[int64]int64{}
	for _, q := range byPkg {
		cursor := q.base
		known := true
		switch {
		case !q.hasHead:
			cursor = now // head already ended; promotion imminent
		case q.base == 0:
			known = false // unlimited-duration head → activation only on exhaustion
		}
		for _, b := range q.queued {
			if known {
				out[b.ID] = cursor
				cursor += b.DurationDays * 86400
			} else {
				out[b.ID] = 0
			}
		}
	}
	return out
}

// buildPlanViews shapes a user's buckets for the UI: each plan + the pool (if it
// has any balance), with remaining traffic and a derived status. The free bucket
// is excluded — it is an internal unmetered metering identity, not a package the
// user bought, so surfacing it as a permanent "不限" row only confuses.
//
// pkgNames maps package id → current name. A plan bucket for a real package
// (PackageID > 0) shows that live name so an admin rename reaches everyone who
// holds it; the stored name is only a snapshot from purchase time. Buckets with
// no package row (pool / welcome id -1 / admin grant id 0) keep their own name.
// Pass nil to fall back to the snapshot everywhere.
// dashTraffic is the control-panel's top-line traffic roll-up.
type dashTraffic struct {
	Total         int64 // metered quota the user owns right now
	Used          int64 // usage counted against Total
	Remaining     int64
	Unlimited     bool  // at least one owned份 has no cap
	UnmeteredUsed int64 // usage on those uncapped份 — real, but not part of any ratio
}

// dashboardTraffic rolls the buckets up into one headline figure.
//
// Excluded: the internal free bucket (unmetered metering identity), queued份
// (paid but not yet usable) and expired份 (past their window, so handleSub
// hands out nothing for them). All three would advertise traffic the user
// cannot actually spend — an expired 100G plan showing as 剩余 100 GB directly
// under a 「套餐已全部到期」 banner is the same lie the queued exclusion exists
// to prevent. Exhausted-but-live份 DO stay in: 100% of a quota the user still
// holds is a fact worth showing.
//
// The subtlety is TrafficLimit == 0, which means "uncapped", not "zero quota".
// Summing such a bucket the naive way adds its Used() to the numerator and
// nothing to the denominator, so a user holding one unlimited plan alongside a
// metered one sees an inflated used-percentage — and once the metered份 ran
// low, a "流量已用尽" banner while an unlimited plan is still live. Keep the
// ratio over capped buckets only and report the uncapped usage on the side.
//
// Display-only: enforcement (handleSub) still reads the buckets directly.
func dashboardTraffic(buckets []*store.Bucket) dashTraffic {
	var d dashTraffic
	now := time.Now().Unix()
	for _, b := range buckets {
		if b.Kind == store.KindFree || (b.Kind == "plan" && b.Status == "queued") {
			continue
		}
		if !b.NotExpired(now) {
			continue
		}
		// An empty pool is inert bookkeeping, not an uncapped份 — buildPlanViews
		// hides it for the same reason. Every account has one, so reading its 0
		// limit as "unlimited" would tell literally everyone their traffic is 不限.
		if b.Kind == "pool" && b.TrafficLimit <= 0 {
			continue
		}
		if b.TrafficLimit <= 0 {
			d.Unlimited = true
			d.UnmeteredUsed += b.Used()
			continue
		}
		d.Total += b.TrafficLimit
		d.Used += b.Used()
	}
	if d.Total > d.Used {
		d.Remaining = d.Total - d.Used
	}
	return d
}

func buildPlanViews(buckets []*store.Bucket, pkgNames map[int64]string) []planView {
	now := time.Now().Unix()
	acts := queueActivations(buckets, now)
	out := []planView{}
	for _, b := range buckets {
		if b.Kind == store.KindFree {
			continue // internal unmetered metering identity, not a user-facing package
		}
		if b.Kind == "pool" && b.TrafficLimit == 0 {
			continue // empty/inert pool — nothing to show
		}
		name := b.Name
		if b.PackageID > 0 {
			if live, ok := pkgNames[b.PackageID]; ok && live != "" {
				name = live
			}
		}
		pv := planView{ID: b.ID, Kind: b.Kind, PackageID: b.PackageID, Name: name, TrafficLimit: b.TrafficLimit,
			Used: b.Used(), ExpiryAt: b.ExpiryAt, Remaining: -1, CreatedAt: b.CreatedAt, OrderID: b.OrderID}
		if b.TrafficLimit > 0 {
			if rem := b.TrafficLimit - b.Used(); rem > 0 {
				pv.Remaining = rem
			} else {
				pv.Remaining = 0
			}
		}
		switch {
		case b.Status == "queued":
			pv.Status = "queued" // a same-package purchase waiting for the head to finish
			pv.ActivateBy = acts[b.ID]
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

	// A revoked link must not outlive its revocation in someone else's cache.
	// Panels commonly sit behind a CDN (or a "cache everything" rule), and a
	// cached 200 for a token the panel already deleted looks exactly like
	// "重置后旧链接还能用". The body is per-user and cheap to regenerate anyway.
	w.Header().Set("Cache-Control", "no-store")
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
	// Tag is the sing-box inbound tag for a self-built node, "" for an external
	// (imported share-link) one. It is the join key to the inbound row, and so to
	// the relay/egress chain behind the node.
	Tag string
}

// computeNodeEntries builds the user's nodes with group attribution: external
// nodes in their accessible groups (raw), plus self-built node links filtered to
// the inbound tags in those groups. If no groups are configured at all, falls
// back to all self-built nodes (zero-config). Deduped by link.
func (a *API) computeNodeEntries(u *store.User) []nodeEntry {
	var out []nodeEntry
	seen := map[string]bool{}
	add := func(l string, gid int64, gname, tag string) {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, nodeEntry{Link: l, GroupID: gid, GroupName: gname, Tag: tag})
		}
	}

	groupIDs, _ := a.st.AccessibleGroupIDs(u)

	if len(groupIDs) == 0 {
		// User has no accessible group (no plan, no free group). Only fall back
		// to all self-built nodes when grouping isn't set up at all (zero-config
		// convenience). Once any group exists, "unassigned" means "no nodes".
		if n, _ := a.st.GroupCount(); n == 0 {
			for _, l := range a.selfBuiltLinks(u) {
				add(l.Link, 0, "", l.Tag)
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
			add(n.ShareLink, n.GroupID, gname[n.GroupID], "")
		case "self_built":
			if n.InboundTag != "" {
				tagGroup[n.InboundTag] = n.GroupID
			}
		}
	}
	if len(tagGroup) > 0 {
		for _, l := range a.selfBuiltLinks(u) {
			if gid, ok := tagGroup[l.Tag]; ok {
				add(l.Link, gid, gname[gid], l.Tag)
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

// ensureSubToken backfills a subscription token for an account that has none.
// The first-boot admin is inserted by Seed without one, so its 订阅 page showed
// an empty URL forever; registration mints a token, so everyone else already
// has one and this is a no-op. Returns the user to read the token from.
func (a *API) ensureSubToken(u *store.User) *store.User {
	if u == nil || (u.SubToken.Valid && u.SubToken.String != "") {
		return u
	}
	token, err := idgen.RandToken(24)
	if err != nil {
		return u
	}
	if _, err := a.st.SetSubTokenIfEmpty(u.ID, token); err != nil {
		return u
	}
	// Re-read instead of patching the struct: a concurrent request may have won
	// and minted a different token, and the loser must serve the winner's.
	if fresh, err := a.st.UserByID(u.ID); err == nil && fresh != nil {
		return fresh
	}
	return u
}

// subURL renders a user's subscription address, or "" when there is no token to
// render. Nil-tolerant: callers reach it with the result of a re-read that can
// legitimately come back empty (a concurrently deleted account), and an empty
// URL is the right answer there — not a panic in the middle of a request.
func (a *API) subURL(r *http.Request, u *store.User) string {
	if u == nil || !u.SubToken.Valid {
		return ""
	}
	return a.publicBase(r) + "/sub/" + u.SubToken.String
}
