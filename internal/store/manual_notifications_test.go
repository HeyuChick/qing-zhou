package store

import (
	"path/filepath"
	"testing"
)

func newManualNotificationStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "manual-notifications.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestCreateManualNotificationSnapshotsAllActiveUsers(t *testing.T) {
	st := newManualNotificationStore(t)
	bound, _ := st.CreateUser(NewUser{Username: "bound", PasswordHash: "x"})
	unbound, _ := st.CreateUser(NewUser{Username: "unbound", PasswordHash: "x"})
	disabled, _ := st.CreateUser(NewUser{Username: "disabled", PasswordHash: "x"})
	_, _ = st.DB().Exec(`UPDATE users SET status='banned' WHERE id=?`, disabled)
	if err := st.BindTelegram(bound, 11, 111, "bound", ""); err != nil {
		t.Fatal(err)
	}

	n, err := st.CreateManualNotification("标题", "正文", "all", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n.Total != 2 || n.Pending != 1 || n.Skipped != 1 {
		t.Fatalf("counts = total %d pending %d skipped %d", n.Total, n.Pending, n.Skipped)
	}
	recipients, err := st.ListManualNotificationRecipients(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 2 {
		t.Fatalf("recipients = %d", len(recipients))
	}
	for _, recipient := range recipients {
		if recipient.UserID == unbound && (recipient.Status != "skipped" || recipient.Error != "未绑定 Telegram") {
			t.Fatalf("unbound = %+v", recipient)
		}
	}
}

func TestManualNotificationRecoveryDoesNotRetrySending(t *testing.T) {
	st := newManualNotificationStore(t)
	uid, _ := st.CreateUser(NewUser{Username: "interrupted", PasswordHash: "x"})
	if err := st.BindTelegram(uid, 33, 333, "", ""); err != nil {
		t.Fatal(err)
	}
	n, err := st.CreateManualNotification("标题", "正文", "selected", []int64{uid}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := st.ClaimManualNotificationRecipient(n.ID); err != nil || claimed == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if err := st.FailInterruptedManualNotifications(); err != nil {
		t.Fatal(err)
	}
	recipients, _ := st.ListManualNotificationRecipients(n.ID)
	if len(recipients) != 1 || recipients[0].Status != "failed" || recipients[0].Error == "" {
		t.Fatalf("recovered recipient = %+v", recipients)
	}
	ids, err := st.ListPendingManualNotificationIDs()
	if err != nil || len(ids) != 0 {
		t.Fatalf("pending ids = %v, %v", ids, err)
	}
}

func TestManualNotificationHistoryKeepsDeliveryResults(t *testing.T) {
	st := newManualNotificationStore(t)
	uid, _ := st.CreateUser(NewUser{Username: "target", PasswordHash: "x"})
	if err := st.BindTelegram(uid, 22, 222, "target", ""); err != nil {
		t.Fatal(err)
	}
	n, err := st.CreateManualNotification("标题", "正文", "selected", []int64{uid}, 1)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimManualNotificationRecipient(n.ID)
	if err != nil || claimed == nil || claimed.UserID != uid {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if err := st.SetManualNotificationRecipientResult(n.ID, uid, "sent", ""); err != nil {
		t.Fatal(err)
	}
	got, err := st.ManualNotificationByID(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sent != 1 || got.Pending != 0 || got.Failed != 0 {
		t.Fatalf("history = %+v", got)
	}
	recipients, _ := st.ListManualNotificationRecipients(n.ID)
	if len(recipients) != 1 || recipients[0].Status != "sent" || recipients[0].SentAt == 0 {
		t.Fatalf("recipient = %+v", recipients)
	}
}
