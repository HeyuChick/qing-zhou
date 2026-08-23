package store

import (
	"errors"
	"testing"
	"time"
)

func TestBindTelegramUniquePerChat(t *testing.T) {
	st := newRefundStore(t)
	a := mkUser(t, st, "alice")
	b := mkUser(t, st, "bob")

	if err := st.BindTelegram(a, 1001, 1001, "alice_tg", "Alice"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(b, 1001, 1001, "alice_tg", "Alice"); !errors.Is(err, ErrTelegramTaken) {
		t.Fatalf("second bind = %v, want ErrTelegramTaken", err)
	}
	got, err := st.TelegramBindByTelegramID(1001)
	if err != nil || got == nil || got.UserID != a {
		t.Fatalf("owner = %+v err=%v, want alice", got, err)
	}
}

func TestBindTelegramReplacesSameUser(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")

	if err := st.BindTelegram(uid, 1001, 1001, "old", "A"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid, 2002, 2002, "new", "A"); err != nil {
		t.Fatal(err)
	}
	if old, _ := st.TelegramBindByTelegramID(1001); old != nil {
		t.Fatal("old telegram id still bound after rebind")
	}
	got, err := st.TelegramBindByUser(uid)
	if err != nil || got == nil || got.TelegramID != 2002 || got.Username != "new" {
		t.Fatalf("rebind = %+v err=%v", got, err)
	}
}

func TestTelegramBindTokenConsumeOnce(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")

	if err := st.CreateTelegramBindToken(uid, "tok-1", time.Hour); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.UseTelegramBindToken("tok-1")
	if err != nil || !ok || got != uid {
		t.Fatalf("first consume = %d ok=%v err=%v", got, ok, err)
	}
	if _, ok, err = st.UseTelegramBindToken("tok-1"); err != nil || ok {
		t.Fatalf("second consume ok=%v err=%v, want used", ok, err)
	}
}

func TestBindTelegramWithTokenRollsBackWhenTaken(t *testing.T) {
	st := newRefundStore(t)
	alice := mkUser(t, st, "alice")
	bob := mkUser(t, st, "bob")
	if err := st.BindTelegram(alice, 1001, 1001, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTelegramBindToken(bob, "bob-token", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.BindTelegramWithToken("bob-token", 1001, 1001, "", ""); !errors.Is(err, ErrTelegramTaken) {
		t.Fatalf("taken bind err=%v", err)
	}
	uid, ok, err := st.BindTelegramWithToken("bob-token", 2002, 2002, "", "")
	if err != nil || !ok || uid != bob {
		t.Fatalf("retry uid=%d ok=%v err=%v", uid, ok, err)
	}
	if _, ok, err := st.BindTelegramWithToken("bob-token", 3003, 3003, "", ""); err != nil || ok {
		t.Fatalf("used token ok=%v err=%v", ok, err)
	}
}

func TestTelegramBindTokenLatestWins(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")

	if err := st.CreateTelegramBindToken(uid, "old", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTelegramBindToken(uid, "new", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.UseTelegramBindToken("old"); ok {
		t.Fatal("superseded token still worked")
	}
	if got, ok, err := st.UseTelegramBindToken("new"); err != nil || !ok || got != uid {
		t.Fatalf("latest token = %d ok=%v err=%v", got, ok, err)
	}
}

func TestTelegramBindTokenExpired(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")
	if err := st.CreateTelegramBindToken(uid, "stale", -time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.UseTelegramBindToken("stale"); err != nil || ok {
		t.Fatalf("expired token ok=%v err=%v", ok, err)
	}
}

func TestClaimNotifyOnceUntilCleared(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")

	ok, err := st.ClaimNotify(uid, "traffic_low", "account")
	if err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	ok, err = st.ClaimNotify(uid, "traffic_low", "account")
	if err != nil || ok {
		t.Fatalf("second claim ok=%v err=%v, want denied", ok, err)
	}
	if err := st.ClearNotify(uid, "traffic_low", "account"); err != nil {
		t.Fatal(err)
	}
	ok, err = st.ClaimNotify(uid, "traffic_low", "account")
	if err != nil || !ok {
		t.Fatalf("after clear ok=%v err=%v, want allowed", ok, err)
	}
}

func TestDeleteUserDropsTelegramRows(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")
	if err := st.BindTelegram(uid, 1001, 1001, "a", "A"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTelegramBindToken(uid, "tok", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNotify(uid, "expired", "b1"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(uid); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.TelegramBindByUser(uid); got != nil {
		t.Fatal("bind survived DeleteUser")
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM telegram_bind_tokens WHERE user_id=?`, uid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tokens left=%d err=%v", n, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_notify_log WHERE user_id=?`, uid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("notify log left=%d err=%v", n, err)
	}
}

func TestUnbindTelegramByTelegramIDReturnsDeletedOwner(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "unbind")
	if err := st.BindTelegram(uid, 42, 42, "", ""); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.UnbindTelegramByTelegramID(42)
	if err != nil || !ok || got != uid {
		t.Fatalf("uid=%d ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := st.UnbindTelegramByTelegramID(42); err != nil || ok {
		t.Fatalf("second ok=%v err=%v", ok, err)
	}
}

func TestSetTelegramNotifyRequiresBind(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")
	if err := st.SetTelegramNotify(uid, false, false); err == nil {
		t.Fatal("set notify on unbound user succeeded")
	}
	if err := st.BindTelegram(uid, 1, 1, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTelegramNotify(uid, false, true); err != nil {
		t.Fatal(err)
	}
	got, _ := st.TelegramBindByUser(uid)
	if got.NotifyExpiry || !got.NotifyTraffic {
		t.Fatalf("prefs = %+v", got)
	}
}
