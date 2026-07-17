package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

// User groups decide who may buy a package. They are unrelated to node groups
// (/api/admin/node-groups), which decide which nodes a package grants.

func (a *API) handleAdminListUserGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := a.st.ListUserGroups()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用户组失败")
		return
	}
	counts, _ := a.st.UserGroupMemberCounts()
	for _, g := range groups {
		g.Members = counts[g.ID]
	}
	ok(w, groups)
}

func validateUserGroup(g *store.UserGroup) string {
	g.Name = strings.TrimSpace(g.Name)
	if g.Name == "" {
		return "用户组名称不能为空"
	}
	if len(g.Name) > 64 {
		return "用户组名称过长"
	}
	return ""
}

func (a *API) handleAdminCreateUserGroup(w http.ResponseWriter, r *http.Request) {
	var g store.UserGroup
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if msg := validateUserGroup(&g); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	id, err := a.st.CreateUserGroup(g)
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建用户组失败")
		return
	}
	created, _ := a.st.GetUserGroup(id)
	ok(w, created)
}

func (a *API) handleAdminUpdateUserGroup(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的用户组 id")
		return
	}
	var g store.UserGroup
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if msg := validateUserGroup(&g); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	g.ID = id
	if err := a.st.UpdateUserGroup(g); err != nil {
		fail(w, http.StatusInternalServerError, "更新用户组失败")
		return
	}
	updated, _ := a.st.GetUserGroup(id)
	ok(w, updated)
}

// DELETE /api/admin/user-groups/{id}[?force=1]
//
// Deleting a group that is some package's only buyer restriction would silently
// make that package public — the opposite of what the admin intends. Refuse and
// name the packages unless force=1.
func (a *API) handleAdminDeleteUserGroup(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的用户组 id")
		return
	}
	if r.URL.Query().Get("force") != "1" {
		orphaned, err := a.st.PackagesRestrictedToOnly(id)
		if err != nil {
			fail(w, http.StatusInternalServerError, "服务器错误")
			return
		}
		if len(orphaned) > 0 {
			names := make([]string, 0, len(orphaned))
			for _, pid := range orphaned {
				if p, _ := a.st.GetPackage(pid); p != nil {
					names = append(names, p.Name)
				}
			}
			fail(w, http.StatusConflict, fmt.Sprintf(
				"套餐「%s」仅限该用户组购买，删除后将对所有人开放。请先给这些套餐改绑其他用户组，或确认后强制删除。",
				strings.Join(names, "」「")))
			return
		}
	}
	if err := a.st.DeleteUserGroup(id); err != nil {
		fail(w, http.StatusInternalServerError, "删除用户组失败")
		return
	}
	ok(w, nil)
}

// GET /api/admin/user-groups/{id}/members — users in the group.
func (a *API) handleAdminUserGroupMembers(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的用户组 id")
		return
	}
	if g, _ := a.st.GetUserGroup(id); g == nil {
		fail(w, http.StatusNotFound, "用户组不存在")
		return
	}
	users, err := a.st.ListUserGroupMembers(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取成员失败")
		return
	}
	ok(w, userGroupMemberViews(users))
}

// PUT /api/admin/user-groups/{id}/members {user_ids} — replace the group's
// membership wholesale, in one transaction.
//
// Membership is a group↔user relation; editing it one user at a time through
// PUT /api/admin/users/{id} would be non-atomic (a mid-way failure leaves the
// group half-applied) and would rebuild sing-box once per user for a change
// that cannot affect sing-box at all.
func (a *API) handleAdminSetUserGroupMembers(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的用户组 id")
		return
	}
	if g, _ := a.st.GetUserGroup(id); g == nil {
		fail(w, http.StatusNotFound, "用户组不存在")
		return
	}
	var req struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.st.SetGroupMembers(id, req.UserIDs); err != nil {
		fail(w, http.StatusInternalServerError, "保存成员失败")
		return
	}
	// No invalidateLinks/sbRebuild here: user groups gate purchases only, and
	// never appear in a sing-box config or a subscription.
	users, err := a.st.ListUserGroupMembers(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取成员失败")
		return
	}
	ok(w, userGroupMemberViews(users))
}

func userGroupMemberViews(users []*store.User) []J {
	out := make([]J, 0, len(users))
	for _, u := range users {
		out = append(out, J{"id": u.ID, "username": u.Username, "email": nsOr(u.Email)})
	}
	return out
}
