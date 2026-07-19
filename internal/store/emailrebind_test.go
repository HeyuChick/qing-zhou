package store

import (
	"testing"
	"time"
)

// A verify token carries only user_id — no address — and SetEmailVerified marks
// whatever address the row currently holds. So a token must not survive a rebind:
// otherwise a user requests a token for an address they own, doesn't click it,
// rebinds to someone else's address, then redeems the old token and lands
// email_verified on an address they never controlled — squatting it permanently,
// since registration and rebinding both reject an address another account holds.
func TestSetUserEmail_InvalidatesOutstandingVerifyTokens(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "mallory")

	if err := st.SetUserEmail(uid, "attacker@evil.test"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateEmailToken(uid, "T1", "verify", time.Hour); err != nil {
		t.Fatal(err)
	}
	// Rebind to a victim address without redeeming T1.
	if err := st.SetUserEmail(uid, "ceo@victim.test"); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := st.UseEmailToken("T1", "verify"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("the pre-rebind verify token still redeems — it would verify the victim's address")
	}
	var verified bool
	st.db.QueryRow(`SELECT email_verified FROM users WHERE id=?`, uid).Scan(&verified)
	if verified {
		t.Error("user is email_verified on an address they never proved control of")
	}
}

// Password-reset tokens are a separate purpose and must not be collateral damage
// of a rebind — a user changing their email mid-reset should still be able to
// finish the reset.
func TestSetUserEmail_KeepsResetTokens(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "rita")
	if err := st.CreateEmailToken(uid, "R1", "reset", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserEmail(uid, "rita@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.UseEmailToken("R1", "reset"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Error("rebinding the email wrongly invalidated an outstanding reset token")
	}
}

// Existing provisioned accounts must gain a free bucket on upgrade — the pool no
// longer carries the free group, so without the backfill they lose free-node
// access. Accounts that were never provisioned must be left alone: inventing an
// identity for one would insert a user into the sing-box config who was never
// meant to be there.
func TestBackfillFreeBuckets(t *testing.T) {
	st := newRefundStore(t)
	provisioned := mkUser(t, st, "olduser")
	if err := st.EnsurePoolBucket(provisioned, "qz_olduser", "uuid", "secret"); err != nil {
		t.Fatal(err)
	}
	unprovisioned := mkUser(t, st, "pending")

	if err := st.backfillFreeBuckets(); err != nil {
		t.Fatal(err)
	}
	if bucketOfKind(t, st, provisioned, KindFree) == nil {
		t.Error("provisioned account did not get a free bucket — it just lost free-node access")
	}
	if bucketOfKind(t, st, unprovisioned, KindFree) != nil {
		t.Error("unprovisioned account should not be given an identity by the backfill")
	}

	// Idempotent: it runs on every start.
	if err := st.backfillFreeBuckets(); err != nil {
		t.Fatal(err)
	}
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM user_plans WHERE user_id=? AND kind=?`, provisioned, KindFree).Scan(&n)
	if n != 1 {
		t.Errorf("got %d free buckets after two runs, want 1", n)
	}
}
