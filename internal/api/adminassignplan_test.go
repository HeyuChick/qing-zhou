package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

// assignPlan posts to the admin assign-plan handler with the target in the path
// and the operator's identity in context. Body is built here because the shared
// adminPost helper sends none.
func assignPlan(a *API, operatorID, targetID, pkgID int64) *httptest.ResponseRecorder {
	body := strings.NewReader(`{"package_id":` + strconv.FormatInt(pkgID, 10) + `}`)
	req := httptest.NewRequest("POST", "/api/admin/users/"+strconv.FormatInt(targetID, 10)+"/assign-plan", body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(targetID, 10))
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	req = req.WithContext(context.WithValue(ctx, ctxUserID, operatorID))
	w := httptest.NewRecorder()
	a.handleAdminAssignPlan(w, req)
	return w
}

// The admin account is an ordinary identity below the API: 积分购买 already lets it
// hold a plan, and BuildUsersByTag provisions it into the inbounds like anyone
// else. Refusing the free comp made the panel owner's own account the one
// account they could not equip from the panel.
func TestAdminAssignPlan_AdminTargetIsAllowed(t *testing.T) {
	a, st := newResetSubAPI(t)
	admin, err := st.CreateUser(store.NewUser{Username: "root", PasswordHash: "x", Role: "admin", SubToken: "T"})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-provision: the handler would otherwise call provisionClient, which needs
	// a live sing-box controller this harness does not have.
	if err := st.SetUserClient(admin, 0, "qz_root", "uuid", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(admin, "qz_root", "uuid", "secret"); err != nil {
		t.Fatal(err)
	}
	pkg, err := st.CreatePackage(store.Package{
		Type: "plan", Name: "月付", PricePoints: 100,
		TrafficBytes: 100 << 30, DurationDays: 30, Stock: -1, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Bind the plan to a node group so the entitlement assertion below is real:
	// a missing free group no longer falls through to "every node".
	gid, err := st.CreateGroup(store.NodeGroup{Name: "香港"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPlanGroups(pkg, []int64{gid}); err != nil {
		t.Fatal(err)
	}
	if gids, _ := st.AccessibleGroupIDs(&store.User{ID: admin}); len(gids) != 0 {
		t.Fatalf("precondition: admin already reaches groups %v before the grant", gids)
	}

	if w := assignPlan(a, admin, admin, pkg); w.Code != http.StatusOK {
		t.Fatalf("assign-plan to an admin = %d %s, want 200", w.Code, w.Body.String())
	}

	buckets, err := st.ListBuckets(admin)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range buckets {
		if b.Kind == "plan" && b.PackageID == pkg {
			found = true
			if b.TrafficLimit != 100<<30 {
				t.Errorf("plan bucket limit = %d, want 100GiB", b.TrafficLimit)
			}
		}
	}
	if !found {
		t.Fatal("no plan bucket landed for the admin — the grant did not take effect")
	}

	// The point of the grant: the admin's identity now reaches the plan's nodes,
	// so its own subscription actually carries something.
	gids, err := st.AccessibleGroupIDs(&store.User{ID: admin})
	if err != nil {
		t.Fatal(err)
	}
	if len(gids) != 1 || gids[0] != gid {
		t.Errorf("admin accessible groups = %v, want [%d] — the grant did not reach enforcement", gids, gid)
	}
}

func TestAdminAssignPlan_UnknownUser(t *testing.T) {
	a, st := newResetSubAPI(t)
	pkg, err := st.CreatePackage(store.Package{Type: "plan", Name: "月付", DurationDays: 30, Stock: -1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if w := assignPlan(a, 1, 4242, pkg); w.Code != http.StatusNotFound {
		t.Errorf("assign-plan for a nonexistent user = %d, want 404", w.Code)
	}
}
