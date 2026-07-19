package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/auth"
	"qingzhou/internal/idgen"
	"qingzhou/internal/store"
)

// handleAdminRebuild regenerates and re-applies the sing-box config from the DB,
// and drops every user's cached subscription links. Use after bulk plan/group
// changes to apply entitlement-based access immediately (the controller's
// periodic loop would otherwise catch up within ~1 minute).
func (a *API) handleAdminRebuild(w http.ResponseWriter, r *http.Request) {
	users, err := a.st.UsersWithClient()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用户失败")
		return
	}
	disabled := 0
	for _, u := range users {
		if u.Role == "admin" {
			continue
		}
		a.invalidateLinks(u.ID)
		if u.Status != "banned" && !a.userHasNodeAccess(u) {
			disabled++
		}
	}
	if err := a.sbRebuild(); err != nil {
		fail(w, http.StatusBadGateway, "重建配置失败: "+err.Error())
		return
	}
	ok(w, J{"total": len(users), "synced": len(users), "disabled_no_access": disabled})
}

// adminUserViewWithGroups renders a user for the admin UI. group_ids is always
// present (never null) so the frontend can bind it to a multi-select without a
// null guard. There is deliberately no group-less variant: one that passed nil
// would report "this user is in no groups", which is a claim, not a default.
func adminUserViewWithGroups(u *store.User, groupIDs []int64) J {
	if groupIDs == nil {
		groupIDs = []int64{}
	}
	return J{
		"id":             u.ID,
		"username":       u.Username,
		"email":          nsOr(u.Email),
		"role":           u.Role,
		"status":         u.Status,
		"email_verified": u.EmailVerified,
		"points":         u.Points,
		"used":           u.UsedUp + u.UsedDown,
		"traffic_limit":  u.TrafficLimit,
		"expiry_at":      u.ExpiryAt,
		"has_client":     u.ClientID.Valid,
		"created_at":     u.CreatedAt,
		"group_ids":      groupIDs,
	}
}

// adminUserViewLoadGroups builds the view for a single user, fetching their
// groups. Use the bulk path for lists.
func (a *API) adminUserViewLoadGroups(u *store.User) J {
	gids, _ := a.st.UserGroupIDs(u.ID)
	return adminUserViewWithGroups(u, gids)
}

// POST /api/admin/users — admin creates a user directly (no registration gate,
// no email verification) and provisions their sing-box client immediately.
func (a *API) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string  `json:"username"`
		Email    string  `json:"email"`
		Password string  `json:"password"`
		Points   int64   `json:"points"`
		GroupIDs []int64 `json:"group_ids"`
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

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	traffic, _ := a.st.GetSettingInt64("default_traffic", 10<<30)
	deviceLimit, _ := a.st.GetSettingInt64("default_device_limit", 3)
	expiryDays, _ := a.st.GetSettingInt64("default_expiry_days", 30)
	subToken, _ := idgen.RandToken(24)
	expiryAt := int64(0)
	if expiryDays > 0 {
		expiryAt = time.Now().Unix() + expiryDays*86400
	}

	id, err := a.st.CreateUser(store.NewUser{
		Username: req.Username, Email: req.Email, PasswordHash: hash,
		SubToken: subToken, TrafficLimit: traffic, DeviceLimit: deviceLimit, ExpiryAt: expiryAt,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建用户失败")
		return
	}
	_ = a.st.SetEmailVerified(id) // admin-created accounts are pre-verified
	_ = a.st.SetUserGroups(id, req.GroupIDs)

	u, _ := a.st.UserByID(id)
	if err := a.provisionClient(u); err != nil {
		_ = a.st.DeleteUser(id)
		fail(w, http.StatusBadGateway, "开通节点失败："+err.Error())
		return
	}
	if req.Points > 0 {
		operatorID, _ := r.Context().Value(ctxUserID).(int64)
		_, _ = a.st.AdjustPoints(id, req.Points, "admin_recharge", operatorID, "管理员开户赠送")
	}

	u, _ = a.st.UserByID(id)
	ok(w, a.adminUserViewLoadGroups(u))
}

// PUT /api/admin/users/{id} — edit a user's quota / expiry / status.
// Fields are optional (pointers); only provided ones change.
func (a *API) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	u, err := a.st.UserByID(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if u == nil {
		fail(w, http.StatusNotFound, "用户不存在")
		return
	}
	var req struct {
		// Manual "general allowance" grant, stored in a real bucket (see
		// AdminUpdateUser). ManualEnabled=false removes it; ManualTraffic 0 = unlimited,
		// ManualExpiry 0 = never. TrafficLimit/ExpiryAt are accepted for backward
		// compatibility with older clients and mapped onto the grant.
		ManualEnabled *bool    `json:"manual_enabled"`
		ManualTraffic *int64   `json:"manual_traffic"`
		ManualExpiry  *int64   `json:"manual_expiry"`
		TrafficLimit  *int64   `json:"traffic_limit"`
		ExpiryAt      *int64   `json:"expiry_at"`
		Status        *string  `json:"status"`
		Password      *string  `json:"password"`
		ResetTraffic  bool     `json:"reset_traffic"`
		GroupIDs      *[]int64 `json:"group_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	// Optional password reset (logs the user out everywhere).
	if req.Password != nil && *req.Password != "" {
		if len(*req.Password) < 6 {
			fail(w, http.StatusBadRequest, "密码至少 6 位")
			return
		}
		hash, herr := auth.HashPassword(*req.Password)
		if herr != nil {
			fail(w, http.StatusInternalServerError, "服务器错误")
			return
		}
		if err := a.st.UpdatePassword(id, hash); err != nil {
			fail(w, http.StatusInternalServerError, "重置密码失败")
			return
		}
		_ = a.st.DeleteUserSessions(id)
	}

	status := u.Status
	if req.Status != nil {
		if *req.Status != "active" && *req.Status != "banned" {
			fail(w, http.StatusBadRequest, "状态只能是 active / banned")
			return
		}
		status = *req.Status
	}

	// Resolve the manual allowance grant. New clients send manual_enabled explicitly;
	// older clients send traffic_limit/expiry_at, which we map onto the grant. When
	// none of these are present the edit only touches status/reset, so leave the grant
	// unchanged (nil) rather than deleting it.
	var manual *store.ManualGrant
	switch {
	case req.ManualEnabled != nil:
		g := store.ManualGrant{Enabled: *req.ManualEnabled}
		if req.ManualTraffic != nil && *req.ManualTraffic >= 0 {
			g.Traffic = *req.ManualTraffic
		}
		if req.ManualExpiry != nil && *req.ManualExpiry >= 0 {
			g.Expiry = *req.ManualExpiry
		}
		manual = &g
	case req.TrafficLimit != nil || req.ExpiryAt != nil:
		g := store.ManualGrant{Enabled: true}
		if req.TrafficLimit != nil && *req.TrafficLimit >= 0 {
			g.Traffic = *req.TrafficLimit
		}
		if req.ExpiryAt != nil && *req.ExpiryAt >= 0 {
			g.Expiry = *req.ExpiryAt
		}
		manual = &g
	}

	if err := a.st.AdminUpdateUser(id, status, req.ResetTraffic, manual); err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	// Group membership only changes when the caller sends the field. Removing a
	// user from a group blocks future buys of that group's packages; plans they
	// already hold keep working until they expire.
	if req.GroupIDs != nil {
		if err := a.st.SetUserGroups(id, *req.GroupIDs); err != nil {
			fail(w, http.StatusInternalServerError, "保存用户组失败")
			return
		}
	}
	// Banning must terminate existing sessions — otherwise the user's already-
	// issued JWT stays valid for up to 7 days (authMiddleware only checks the
	// session exists, not the user's status). Re-login is blocked by handleLogin.
	if status == "banned" {
		_ = a.st.DeleteUserSessions(id)
	}
	a.invalidateLinks(id)
	a.sbRebuildLog()
	out, _ := a.st.UserByID(id)
	ok(w, a.adminUserViewLoadGroups(out))
}

// POST /api/admin/users/{id}/assign-plan {package_id} — admin grants a package
// to a user without charging points (manual activation / comp). Applies the same
// entitlement as a purchase and pushes it to sing-box.
func (a *API) handleAdminAssignPlan(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	var req struct {
		PackageID int64 `json:"package_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PackageID <= 0 {
		fail(w, http.StatusBadRequest, "请求格式错误")
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
		fail(w, http.StatusBadRequest, "管理员账号无需开通套餐")
		return
	}
	pkg, err := a.st.GetPackage(req.PackageID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if pkg == nil {
		fail(w, http.StatusNotFound, "套餐不存在")
		return
	}

	// Ensure the user has a proxy identity before granting a plan.
	if !u.ClientID.Valid {
		if err := a.provisionClient(u); err != nil {
			fail(w, http.StatusBadGateway, "开通节点失败："+err.Error())
			return
		}
		u, _ = a.st.UserByID(id) // refetch with the new client id
	}

	operatorID, _ := r.Context().Value(ctxUserID).(int64)
	if _, err := a.st.AssignPackage(id, pkg, operatorID, a.syncEntitlement); err != nil {
		switch {
		case err == store.ErrUnknownPkgType:
			fail(w, http.StatusBadRequest, "未知套餐类型")
		default:
			fail(w, http.StatusBadGateway, "开通失败，已回滚："+err.Error())
		}
		return
	}
	a.invalidateLinks(id)
	a.sbRebuildLog()
	out, _ := a.st.UserByID(id)
	ok(w, a.adminUserViewLoadGroups(out))
}

// GET /api/admin/users?q=
func (a *API) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.st.ListUsers(r.URL.Query().Get("q"), 200)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用户失败")
		return
	}
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	groups, err := a.st.UserGroupIDsBulk(ids) // one query, not one per user
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用户组失败")
		return
	}
	out := make([]J, 0, len(users))
	for _, u := range users {
		out = append(out, adminUserViewWithGroups(u, groups[u.ID]))
	}
	ok(w, out)
}
