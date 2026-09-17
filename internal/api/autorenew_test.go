package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

func autoRenewRequest(userID, bucketID int64, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPut, "/api/user/plans/"+strconv.FormatInt(bucketID, 10)+"/auto-renew", strings.NewReader(body))
	route := chi.NewRouteContext()
	route.URLParams.Add("id", strconv.FormatInt(bucketID, 10))
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, route)
	ctx = context.WithValue(ctx, ctxUserID, userID)
	return req.WithContext(ctx)
}

func TestUserPlanAutoRenewHandlerOwnsOnlyCurrentUsersLine(t *testing.T) {
	a, st := newUserEditAPI(t)
	ownerID, err := st.CreateUser(store.NewUser{Username: "renew-owner", PasswordHash: "x", Points: 500})
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := st.CreateUser(store.NewUser{Username: "renew-other", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	pkgID, err := st.CreatePackage(store.Package{Type: "plan", Name: "30 天套餐", PricePoints: 100, TrafficBytes: 100 << 30, DurationDays: 30, Stock: -1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := st.GetPackage(pkgID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Purchase(ownerID, pkg, "", nil); err != nil {
		t.Fatal(err)
	}
	buckets, err := st.ListBuckets(ownerID)
	if err != nil || len(buckets) != 1 {
		t.Fatalf("buckets=%#v err=%v", buckets, err)
	}

	w := httptest.NewRecorder()
	a.handleUserPlanAutoRenew(w, autoRenewRequest(ownerID, buckets[0].ID, `{"enabled":false}`))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	updated, err := st.ListBuckets(ownerID)
	if err != nil || updated[0].AutoRenew {
		t.Fatalf("bucket auto_renew=%v err=%v, want false", updated[0].AutoRenew, err)
	}

	w = httptest.NewRecorder()
	a.handleUserPlanAutoRenew(w, autoRenewRequest(otherID, buckets[0].ID, `{"enabled":true}`))
	if w.Code != http.StatusNotFound {
		t.Fatalf("other user status=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	a.handleUserPlanAutoRenew(w, autoRenewRequest(ownerID, buckets[0].ID, `{}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestBuildPlanViewsIncludesAutoRenew(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "plans.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	userID, err := st.CreateUser(store.NewUser{Username: "plan-view", PasswordHash: "x", Points: 500})
	if err != nil {
		t.Fatal(err)
	}
	packageID, err := st.CreatePackage(store.Package{Type: "plan", Name: "套餐", PricePoints: 100, TrafficBytes: 100 << 30, DurationDays: 30, Stock: -1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := st.GetPackage(packageID)
	if _, err := st.Purchase(userID, pkg, "", nil); err != nil {
		t.Fatal(err)
	}
	buckets, _ := st.ListBuckets(userID)
	if err := st.SetPlanAutoRenew(userID, buckets[0].ID, false); err != nil {
		t.Fatal(err)
	}
	buckets, _ = st.ListBuckets(userID)
	views := buildPlanViews(buckets, nil)
	if len(views) != 1 || views[0].AutoRenew {
		t.Fatalf("plan views=%#v, want auto_renew=false", views)
	}
}
