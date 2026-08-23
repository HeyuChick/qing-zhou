package store

import (
	"testing"
	"time"
)

// A missing free-group setting must not dump every inbound onto every user
// that happens to have a live bucket. That was the zero-config fallback.
func TestBuildUsersByTag_NoGroupsMeansNobody(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "bare")
	pkg := mkPlan(t, st, "月付", 10, 100, 30)
	buy(t, st, uid, pkg)
	if _, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "in-all", Listen: "::", ListenPort: 8443,
		Options: "{}", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	byTag, err := st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if got := byTag["in-all"]; len(got) != 0 {
		t.Fatalf("no free group and no node groups must not inject users into inbounds, got %d", len(got))
	}

	bindPlanToInbound(t, st, pkg.ID, "in-all")
	byTag, err = st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if len(byTag["in-all"]) == 0 {
		t.Fatal("binding the plan to the inbound's group must start injecting the buyer")
	}
}
