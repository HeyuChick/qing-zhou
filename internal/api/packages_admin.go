package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

var validPkgTypes = map[string]bool{"traffic": true, "plan": true}

// validatePackage rejects nonsensical/negative package fields. A negative price
// would credit points on "purchase"; a negative traffic would reduce quota while
// charging; a plan with no duration never expires by accident.
func validatePackage(p *store.Package) string {
	if !validPkgTypes[p.Type] {
		return "商品类型必须为 traffic / plan"
	}
	if p.Name == "" {
		return "商品名称不能为空"
	}
	if p.PricePoints < 0 {
		return "价格不能为负"
	}
	if p.TrafficBytes < 0 {
		return "流量不能为负"
	}
	if p.DurationDays < 0 {
		return "有效期不能为负"
	}
	if p.Type == "plan" && p.DurationDays <= 0 {
		return "订阅套餐必须设置正的有效期（天）"
	}
	return ""
}

func (a *API) handleAdminListPackages(w http.ResponseWriter, r *http.Request) {
	pkgs, err := a.st.ListPackages(false)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取商品失败")
		return
	}
	subs, _ := a.st.PlanSubscriberCounts()
	for _, p := range pkgs {
		if p.Type == "plan" {
			p.GroupIDs, _ = a.st.PlanGroupIDs(p.ID)
		}
		p.Subscribers = subs[p.ID]
	}
	ok(w, pkgs)
}

func (a *API) handleAdminCreatePackage(w http.ResponseWriter, r *http.Request) {
	var p store.Package
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if msg := validatePackage(&p); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	if p.Stock == 0 {
		p.Stock = -1 // default unlimited
	}
	id, err := a.st.CreatePackage(p)
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建商品失败")
		return
	}
	if p.Type == "plan" {
		_ = a.st.SetPlanGroups(id, p.GroupIDs)
	}
	created, _ := a.st.GetPackage(id)
	if created != nil && created.Type == "plan" {
		created.GroupIDs, _ = a.st.PlanGroupIDs(id)
	}
	ok(w, created)
}

func (a *API) handleAdminUpdatePackage(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的商品 id")
		return
	}
	var p store.Package
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if msg := validatePackage(&p); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	p.ID = id
	if err := a.st.UpdatePackage(p); err != nil {
		fail(w, http.StatusInternalServerError, "更新商品失败")
		return
	}
	if p.Type == "plan" {
		_ = a.st.SetPlanGroups(id, p.GroupIDs)
	}
	updated, _ := a.st.GetPackage(id)
	if updated != nil && updated.Type == "plan" {
		updated.GroupIDs, _ = a.st.PlanGroupIDs(id)
	}
	ok(w, updated)
}

func (a *API) handleAdminDeletePackage(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的商品 id")
		return
	}
	// Guard: never hard-delete a package that users are still subscribed to —
	// it would dangle their plan and break the dashboard. Force "下架" first.
	if subs, _ := a.st.PlanSubscribers(id); len(subs) > 0 {
		fail(w, http.StatusBadRequest,
			fmt.Sprintf("该商品仍有 %d 位用户订阅，请先「下架」（退款并清空其套餐）后再删除", len(subs)))
		return
	}
	if err := a.st.DeletePackage(id); err != nil {
		fail(w, http.StatusInternalServerError, "删除商品失败")
		return
	}
	ok(w, nil)
}

// POST /api/admin/packages/{id}/retire — 下架: disable the package, and for every
// user still on this plan refund their latest purchase (points + entitlement)
// and clear their plan. Data (orders) is kept, marked refunded.
func (a *API) handleAdminRetirePackage(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的商品 id")
		return
	}
	operatorID, _ := r.Context().Value(ctxUserID).(int64)
	subs, _ := a.st.PlanSubscribers(id)
	refunded, cleared := 0, 0
	for _, uid := range subs {
		if oid, _ := a.st.LatestRefundableOrderForPackage(uid, id); oid > 0 {
			// Retire refunds prorated too: a subscriber who already consumed most of
			// their traffic/time gets back only the unused remainder, not the full
			// price. Use "" so the store's configured policy applies.
			if _, _, err := a.st.RefundOrder(oid, operatorID, "", a.syncEntitlement); err == nil {
				refunded++
			}
		}
		if err := a.st.SetUserPlan(uid, 0); err == nil {
			cleared++
		}
		a.invalidateLinks(uid)
	}
	if err := a.st.SetPackageEnabled(id, false); err != nil {
		fail(w, http.StatusInternalServerError, "下架失败")
		return
	}
	_ = a.sbRebuild()
	ok(w, J{"subscribers": len(subs), "refunded": refunded, "cleared": cleared})
}

// POST /api/admin/packages/{id}/enable — 上架: re-enable a package for sale. Does
// not re-grant anything to past subscribers.
func (a *API) handleAdminEnablePackage(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的商品 id")
		return
	}
	if err := a.st.SetPackageEnabled(id, true); err != nil {
		fail(w, http.StatusInternalServerError, "上架失败")
		return
	}
	ok(w, nil)
}

// POST /api/admin/users/{id}/points {amount, note}
func (a *API) handleAdminRecharge(w http.ResponseWriter, r *http.Request) {
	uid := atoi(chi.URLParam(r, "id"))
	if uid <= 0 {
		fail(w, http.StatusBadRequest, "无效的用户 id")
		return
	}
	var req struct {
		Amount int64  `json:"amount"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Amount == 0 {
		fail(w, http.StatusBadRequest, "金额不能为空")
		return
	}
	operatorID, _ := r.Context().Value(ctxUserID).(int64)
	txType := "admin_recharge"
	if req.Amount < 0 {
		txType = "adjust"
	}
	balance, err := a.st.AdjustPoints(uid, req.Amount, txType, operatorID, req.Note)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrUserNotFound):
			fail(w, http.StatusNotFound, "用户不存在")
		case errors.Is(err, store.ErrNegativeBalance):
			fail(w, http.StatusBadRequest, "扣减后积分会为负，操作被拒绝")
		default:
			fail(w, http.StatusInternalServerError, "操作失败")
		}
		return
	}
	ok(w, J{"user_id": uid, "balance": balance})
}

// GET /api/admin/orders?q=  — all orders (joined with username), optional search.
func (a *API) handleAdminListOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := a.st.ListOrdersAdmin(r.URL.Query().Get("q"), 300)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取订单失败")
		return
	}
	ok(w, orders)
}

// GET /api/admin/users/{id}/orders — one user's consumption records.
func (a *API) handleAdminUserOrders(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	orders, err := a.st.ListOrders(id, 200)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取订单失败")
		return
	}
	ok(w, orders)
}

// GET /api/admin/users/{id}/plans — one user's independently-metered buckets.
func (a *API) handleAdminUserPlans(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	buckets, err := a.st.ListBuckets(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取套餐失败")
		return
	}
	ok(w, buildPlanViews(buckets))
}

// DELETE /api/admin/orders/{id} — purge an order record. Only allowed for
// orphaned orders (the user has been deleted); active users' orders should be
// refunded, not silently dropped.
func (a *API) handleAdminDeleteOrder(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	o, _ := a.st.GetOrder(id)
	if o == nil {
		fail(w, http.StatusNotFound, "订单不存在")
		return
	}
	if a.st.UserExists(o.UserID) {
		fail(w, http.StatusBadRequest, "该用户仍存在，请用退款而非删除记录")
		return
	}
	if err := a.st.DeleteOrder(id); err != nil {
		fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	ok(w, J{"deleted": id})
}

// GET /api/admin/orders/{id}/refund-preview?mode= — compute (read-only) what a
// refund would return under the current policy, so the admin can confirm the
// prorated amount before acting. mode: ""|prorated|full.
func (a *API) handleAdminRefundPreview(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	q, err := a.st.RefundPreview(id, r.URL.Query().Get("mode"))
	if err != nil {
		if errors.Is(err, store.ErrOrderNotFound) {
			fail(w, http.StatusNotFound, "订单不存在")
			return
		}
		fail(w, http.StatusInternalServerError, "计算退款失败")
		return
	}
	ok(w, q)
}

// POST /api/admin/orders/{id}/refund — refund a purchase: return points (prorated
// to the unused portion by default), undo the entitlement, mark the order
// 'refunded' (record kept). Body/query mode: ""|prorated|full.
func (a *API) handleAdminRefundOrder(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	operatorID, _ := r.Context().Value(ctxUserID).(int64)
	o, _ := a.st.GetOrder(id)
	if o == nil {
		fail(w, http.StatusNotFound, "订单不存在")
		return
	}
	if !a.st.UserExists(o.UserID) {
		fail(w, http.StatusBadRequest, "用户已删除，无法退款（可删除该记录）")
		return
	}
	// mode may come from the query string or a small JSON body; both optional.
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		var body struct {
			Mode string `json:"mode"`
		}
		if json.NewDecoder(r.Body).Decode(&body) == nil {
			mode = body.Mode
		}
	}
	updated, quote, err := a.st.RefundOrder(id, operatorID, mode, a.syncEntitlement)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrAlreadyRefunded):
			fail(w, http.StatusConflict, "该订单已退款")
		case errors.Is(err, store.ErrOrderNotFound):
			fail(w, http.StatusNotFound, "订单不存在")
		default:
			fail(w, http.StatusBadGateway, "退款失败，已回滚："+err.Error())
		}
		return
	}
	a.invalidateLinks(o.UserID)
	_ = a.sbRebuild()
	ok(w, J{"order_id": id, "user_id": o.UserID, "points": updated.Points,
		"refund_points": quote.RefundPoints, "refund_ratio": quote.Ratio,
		"traffic_total": updated.TrafficLimit, "expiry_at": updated.ExpiryAt})
}
