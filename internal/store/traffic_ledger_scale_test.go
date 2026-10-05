package store

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// Keep the same immutable fixture before and after SQL changes. Counts come
// from the real driver; all poll/binding/observation evidence remains present.
func TestTrafficLedgerSQLScale(t *testing.T) {
	if os.Getenv("QZ_METERING_SCALE") != "1" {
		t.Skip("set QZ_METERING_SCALE=1")
	}
	for _, size := range []int{100, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			st := newRefundStore(t)
			_, err := st.db.Exec(`INSERT INTO relay_metering_links(id,source_server_id,source_inbound_id,target_server_id,target_inbound_id,identity_name,credential,created_at,source_name,target_name,spec_hash) VALUES(1,1,1,2,2,'scale-link','unused',1,'entry','landing','fixture')`)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]UsageDelta{}
			for i := 0; i < size; i++ {
				name := fmt.Sprintf("scale-relay-%04d", i)
				if _, err = st.db.Exec(`INSERT INTO relay_metering_users(link_id,user_id,generation,identity_name,credential,created_at) VALUES(1,?,1,?,'unused',1)`, i+1, name); err != nil {
					t.Fatal(err)
				}
				values[name] = UsageDelta{Up: 100, Down: 1000}
			}
			count := meterSQL(t, st)
			now := time.Now().Unix()
			for i, label := range []string{"positive", "zero", "positive_again", "new_epoch"} {
				if i == 2 {
					for k, v := range values {
						v.Down += 10
						values[k] = v
					}
				}
				epoch := "scale-epoch"
				if i == 3 {
					epoch = "next-epoch"
				}
				p := TrafficPoll{ID: fmt.Sprintf("scale-%d", i), ServerID: 2, ObservedAt: now + int64(i), Mode: "cumulative", Epoch: epoch, Traffic: values}
				count.Store(0)
				started := time.Now()
				if _, err = st.RecordTrafficPoll(p); err != nil {
					t.Fatal(err)
				}
				elapsed, sqlCount := time.Since(started), count.Load()
				var bindings, observations int
				if err = st.db.QueryRow(`SELECT COUNT(*) FROM traffic_poll_bindings WHERE poll_id=?`, p.ID).Scan(&bindings); err != nil {
					t.Fatal(err)
				}
				if err = st.db.QueryRow(`SELECT COUNT(*) FROM traffic_observations WHERE poll_id=?`, p.ID).Scan(&observations); err != nil {
					t.Fatal(err)
				}
				if bindings != size || observations != size {
					t.Fatalf("evidence lost: bindings=%d observations=%d", bindings, observations)
				}
				t.Logf("%s counters=%d elapsed=%s SQL=%d bindings=%d observations=%d", label, size, elapsed, sqlCount, bindings, observations)
				count.Store(0)
				started = time.Now()
				if _, err = st.RecordTrafficPoll(p); err != nil {
					t.Fatal(err)
				}
				t.Logf("%s replay elapsed=%s SQL=%d", label, time.Since(started), count.Load())
			}
		})
	}
}

func TestTrafficLedgerBatchRelayZeroRetryKeepsFrozenOwner(t *testing.T) {
	st := newRefundStore(t)
	if _, err := st.db.Exec(`INSERT INTO relay_metering_links(id,source_server_id,source_inbound_id,target_server_id,target_inbound_id,identity_name,credential,created_at,source_name,target_name,spec_hash) VALUES(1,1,1,2,2,'batch-link','unused',1,'entry','landing','fixture'); INSERT INTO relay_metering_users(link_id,user_id,generation,identity_name,credential,created_at) VALUES(1,11,1,'batch-a','unused',1),(1,12,1,'batch-b','unused',1); CREATE TRIGGER fail_batch_b BEFORE INSERT ON traffic_observations WHEN NEW.counter_name='batch-b' BEGIN SELECT RAISE(FAIL,'fixture observation failure'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	p := TrafficPoll{ID: "batch-zero", ServerID: 2, ObservedAt: now, Mode: "cumulative", Epoch: "batch-epoch", Traffic: map[string]UsageDelta{"batch-a": {}, "batch-b": {}}}
	if _, err := st.RecordTrafficPoll(p); err == nil {
		t.Fatal("injected failure ignored")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM traffic_poll_bindings WHERE poll_id=?`, p.ID); n != 2 {
		t.Fatal("incomplete frozen journal")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM traffic_observations WHERE poll_id=?`, p.ID); n != 1 {
		t.Fatal("partial zero markers missing")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM traffic_counter_cursors WHERE counter_name='batch-b'`); n != 0 {
		t.Fatal("failed identity advanced cursor")
	}
	later := TrafficPoll{ID: "batch-later", ServerID: 2, ObservedAt: now + 1, Mode: "cumulative", Epoch: "batch-epoch", Traffic: map[string]UsageDelta{"batch-a": {Down: 3}, "batch-b": {Down: 7}}}
	if _, err := st.RecordTrafficPoll(later); err == nil {
		t.Fatal("later identity overtook pending zero")
	}
	if _, err := st.db.Exec(`DROP TRIGGER fail_batch_b; UPDATE relay_metering_users SET user_id=99 WHERE identity_name='batch-b'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RetryPendingTrafficPolls(); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, st, `SELECT user_id FROM traffic_observations WHERE poll_id='batch-zero' AND counter_name='batch-b'`); n != 12 {
		t.Fatal("zero owner rebound during retry")
	}
	if n := scalar(t, st, `SELECT user_id FROM traffic_observations WHERE poll_id='batch-later' AND counter_name='batch-b'`); n != 12 {
		t.Fatal("positive owner rebound during retry")
	}
	if n := scalar(t, st, `SELECT SUM(down) FROM machine_traffic_daily`); n != 10 {
		t.Fatal("retry double counted or lost delta")
	}
	wrong := NewTrafficPoll(3, map[string]UsageDelta{"batch-a": {Down: 9}})
	if _, err := st.RecordTrafficPoll(wrong); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := st.db.QueryRow(`SELECT source_kind FROM traffic_observations WHERE poll_id=?`, wrong.ID).Scan(&kind); err != nil || kind != "unknown" {
		t.Fatalf("source mismatch lost: %s %v", kind, err)
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM traffic_metering_gaps WHERE poll_id=? AND reason='relay_source_mismatch'`, wrong.ID); n != 1 {
		t.Fatal("batch source mismatch gap missing")
	}
}
