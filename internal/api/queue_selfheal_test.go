package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

// The reported failure, end to end: a user's套餐 expires with the next one
// already paid for and queued, and nothing promotes it — so the panel keeps
// showing an expired套餐 and the subscription keeps answering with no nodes,
// leaving the user unable to use a plan they own.
//
// The ticker is deliberately never started here. That is the point: a user must
// not depend on a background sweep having reached them. Reading their own
// dashboard, or their client fetching the subscription, has to be enough.
func TestQueueSelfHeal_ExpiredHeadPromotesOnRead(t *testing.T) {
	a, st := newResetSubAPI(t)

	uid, err := st.CreateUser(store.NewUser{
		Username: "gina", PasswordHash: "x", SubToken: "tok-gina", Points: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	pkgID, err := st.CreatePackage(store.Package{
		Type: "plan", Name: "100G/30d", PricePoints: 100,
		TrafficBytes: 100 << 30, DurationDays: 30, Stock: -1, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := st.GetPackage(pkgID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // one active head + one queued behind it
		if _, err := st.Purchase(uid, pkg, "", func(*store.User, bool) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}

	// The head runs out. Nobody sweeps.
	now := time.Now().Unix()
	if _, err := st.DB().Exec(`UPDATE user_plans SET expiry_at=? WHERE user_id=? AND status='active' AND kind='plan'`,
		now-3600, uid); err != nil {
		t.Fatal(err)
	}

	// What the user sees in the panel.
	req := httptest.NewRequest("GET", "/api/user/dashboard", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxUserID, uid))
	w := httptest.NewRecorder()
	a.handleDashboard(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard = %d %s", w.Code, w.Body.String())
	}

	buckets, _ := st.ListBuckets(uid)
	if got := planStatuses(buckets)["queued"]; got != 0 {
		t.Fatalf("%d份 still queued after the user opened their dashboard — "+
			"an expired套餐 with a paid份 behind it must not stay stuck", got)
	}
	fresh, _ := st.UserByID(uid)
	if fresh.ExpiryAt <= now {
		t.Fatalf("users.expiry_at = %d, still in the past — the panel would keep showing 已到期", fresh.ExpiryAt)
	}

	// And what the user's proxy client sees: a subscription that actually serves.
	sub := httptest.NewRequest("GET", "/sub/tok-gina?format=info", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", "tok-gina")
	sub = sub.WithContext(context.WithValue(sub.Context(), chi.RouteCtxKey, rctx))
	sw := httptest.NewRecorder()
	a.handleSub(sw, sub)
	if sw.Code != http.StatusOK {
		t.Fatalf("sub = %d", sw.Code)
	}
	if body := sw.Body.String(); strings.Contains(body, "套餐已到期") {
		t.Fatal("subscription page still says 套餐已到期 after the queue advanced")
	}
}

// A subscription fetch alone (no panel visit) must repair the queue too — a
// client that stopped working is the most likely first contact.
func TestQueueSelfHeal_SubscriptionFetchPromotes(t *testing.T) {
	a, st := newResetSubAPI(t)

	uid, err := st.CreateUser(store.NewUser{
		Username: "hugo", PasswordHash: "x", SubToken: "tok-hugo", Points: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	pkgID, err := st.CreatePackage(store.Package{
		Type: "plan", Name: "50G/30d", PricePoints: 100,
		TrafficBytes: 50 << 30, DurationDays: 30, Stock: -1, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := st.GetPackage(pkgID)
	for i := 0; i < 2; i++ {
		if _, err := st.Purchase(uid, pkg, "", func(*store.User, bool) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	// This time the head is exhausted rather than expired — the other way a slot
	// frees up.
	if _, err := st.DB().Exec(`UPDATE user_plans SET used_down=traffic_limit
		WHERE user_id=? AND status='active' AND kind='plan'`, uid); err != nil {
		t.Fatal(err)
	}

	sub := httptest.NewRequest("GET", "/sub/tok-hugo", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", "tok-hugo")
	sub = sub.WithContext(context.WithValue(sub.Context(), chi.RouteCtxKey, rctx))
	a.handleSub(httptest.NewRecorder(), sub)

	buckets, _ := st.ListBuckets(uid)
	if got := planStatuses(buckets)["queued"]; got != 0 {
		t.Fatalf("%d份 still queued after a subscription fetch", got)
	}
}

func planStatuses(bs []*store.Bucket) map[string]int {
	out := map[string]int{}
	for _, b := range bs {
		if b.Kind == "plan" {
			out[b.Status]++
		}
	}
	return out
}
