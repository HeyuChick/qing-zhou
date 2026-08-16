package store

import (
	"fmt"
	"testing"
	"time"
)

// A user whose queue advance fails must not stop every other user's queue from
// advancing. The sweep runs for the whole panel, so a single failing transaction
// used to leave everyone behind that user in id order sitting on an expired
// 套餐 with a paid份 queued — forever, because the next tick started from the same
// user and failed the same way.
func TestQueue_OneUserFailureDoesNotStallTheRest(t *testing.T) {
	st := newRefundStore(t)
	bad := mkUser(t, st, "bad")
	good := mkUser(t, st, "good")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	buy(t, st, bad, pkg)
	buy(t, st, bad, pkg)
	buy(t, st, good, pkg)
	buy(t, st, good, pkg)

	now := time.Now().Unix()
	expireActive(t, st, bad, now-3600)
	expireActive(t, st, good, now-3600)

	// Make the FIRST user's promotion fail. A trigger rejecting the promoting
	// UPDATE stands in for whatever makes one user's transaction error in
	// production (SQLITE_BUSY under stats-poll contention, a constraint, ...).
	if _, err := st.db.Exec(fmt.Sprintf(`CREATE TRIGGER boom BEFORE UPDATE OF status ON user_plans
		WHEN NEW.user_id=%d AND NEW.status='active'
		BEGIN SELECT RAISE(ABORT,'boom'); END`, bad)); err != nil {
		t.Fatal(err)
	}

	changed, err := st.AdvanceAllQueues()
	if err == nil {
		t.Fatal("want an error reporting the failed user")
	}
	if len(changed) != 1 || changed[0] != good {
		t.Fatalf("changed = %v, want [%d]: one user's failure must not skip the rest", changed, good)
	}
	if got := planStatusCount(t, st, good, "queued"); got != 0 {
		t.Fatalf("good user still has %d queued份 — stalled behind another user's failure", got)
	}
}

// AdvanceQueueFor is what the read path calls. It must activate a due份 for this
// one user, and must not disturb a user whose current套餐 is still fine.
func TestQueue_AdvanceQueueForOneUser(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "pam")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)

	// Head still usable → nothing due, nothing touched.
	changed, err := st.AdvanceQueueFor(uid)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("promoted while the current套餐 is still usable")
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 1 {
		t.Fatalf("queued份 = %d, want 1 (still waiting its turn)", got)
	}

	// Head expires → the next份 activates on demand, with a fresh countdown.
	now := time.Now().Unix()
	expireActive(t, st, uid, now-3600)
	changed, err = st.AdvanceQueueFor(uid)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expired套餐 with a paid份 queued behind it was not promoted")
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 0 {
		t.Fatalf("queued份 = %d after promotion, want 0", got)
	}
	// The newest active plan bucket is the one just promoted; the retired head is
	// still there with its past date, so pick by MAX rather than by usage (both
	// have none).
	var expiry int64
	if err := st.db.QueryRow(`SELECT MAX(expiry_at) FROM user_plans
		WHERE user_id=? AND kind='plan' AND status='active'`, uid).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if wantMin := now + 29*86400; expiry < wantMin {
		t.Fatalf("promoted expiry=%d, want >= now+~30d (%d) — duration starts at activation", expiry, wantMin)
	}
	// The user's headline expiry must follow, or the panel keeps showing the
	// retired套餐's past date.
	u, _ := st.UserByID(uid)
	if u.ExpiryAt <= now {
		t.Fatalf("users.expiry_at = %d, still in the past after promotion", u.ExpiryAt)
	}

	// Idempotent: nothing left to do.
	if changed, err := st.AdvanceQueueFor(uid); err != nil || changed {
		t.Fatalf("second call changed=%v err=%v, want false/nil", changed, err)
	}
}
