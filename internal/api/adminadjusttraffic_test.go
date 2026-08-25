package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

const giB = 1024 * 1024 * 1024

func adjustPlanTraffic(a *API, uid, planID, delta int64) *httptest.ResponseRecorder {
	body := `{"delta_bytes":` + strconv.FormatInt(delta, 10) + `}`
	req := httptest.NewRequest("POST",
		"/api/admin/users/"+strconv.FormatInt(uid, 10)+"/plans/"+strconv.FormatInt(planID, 10)+"/traffic",
		strings.NewReader(body))
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", strconv.FormatInt(uid, 10))
	rc.URLParams.Add("planID", strconv.FormatInt(planID, 10))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	w := httptest.NewRecorder()
	a.handleAdminAdjustUserPlanTraffic(w, req)
	return w
}

func TestAdminAdjustUserPlanTraffic_AddsQuota(t *testing.T) {
	a, st := newUserEditAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "ada", PasswordHash: "h", Points: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	pkgID, err := st.CreatePackage(store.Package{
		Type: "plan", Name: "月付", PricePoints: 100,
		TrafficBytes: 100 * giB, DurationDays: 30, Stock: -1, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := st.GetPackage(pkgID)
	if _, err := st.Purchase(uid, pkg, "", nil); err != nil {
		t.Fatal(err)
	}
	bs, err := st.ListBuckets(uid)
	if err != nil || len(bs) == 0 {
		t.Fatalf("setup buckets: %v %#v", err, bs)
	}
	var plan *store.Bucket
	for _, b := range bs {
		if b.Kind == "plan" && b.PackageID == pkgID {
			plan = b
			break
		}
	}
	if plan == nil {
		t.Fatal("no plan bucket after purchase")
	}

	w := adjustPlanTraffic(a, uid, plan.ID, 25*giB)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			TrafficLimit int64 `json:"traffic_limit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.TrafficLimit != 125*giB {
		t.Errorf("traffic_limit = %d GiB, want 125", resp.Data.TrafficLimit/giB)
	}
}

func TestAdminAdjustUserPlanTraffic_RejectsZeroAndMissing(t *testing.T) {
	a, st := newUserEditAPI(t)
	uid, _ := st.CreateUser(store.NewUser{Username: "bea", PasswordHash: "h"})

	if w := adjustPlanTraffic(a, uid, 1, 0); w.Code != 400 {
		t.Errorf("zero delta = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := adjustPlanTraffic(a, uid, 999, giB); w.Code != 404 {
		t.Errorf("missing plan = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := adjustPlanTraffic(a, 999, 1, giB); w.Code != 404 {
		t.Errorf("missing user = %d, want 404: %s", w.Code, w.Body.String())
	}
}
