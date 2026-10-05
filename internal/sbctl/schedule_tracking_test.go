package sbctl

import (
	"errors"
	"sync"
	"testing"
)

func TestTrackedRebuildReturnsEpochAndMonotonicRevision(t *testing.T) {
	c := New(schedFakeStore{}, &slowApplier{}, nil, "{}", "")
	first := c.ScheduleRebuildTracked()
	waitDrained(t, c)
	status := c.SyncStatuses()[AllTarget]
	if first.Epoch == "" || first.Revision == 0 || first.Epoch != c.SyncEpoch() {
		t.Fatalf("invalid ticket: %+v", first)
	}
	if status.State != "ok" || status.RequestRevision != first.Revision || status.StartedRevision <= first.Revision || status.Revision <= status.StartedRevision {
		t.Fatalf("untracked outcome: %+v", status)
	}
	if node := c.SyncStatuses()[0]; node.State != "ok" || node.Revision <= status.StartedRevision {
		t.Fatalf("old node result reused: %+v", node)
	}
	second := c.ScheduleRebuildTracked()
	waitDrained(t, c)
	if second.Epoch != first.Epoch || second.Revision <= status.Revision {
		t.Fatalf("reused ticket %+v after %+v", second, status)
	}
	fresh := New(schedFakeStore{}, &slowApplier{}, nil, "{}", "")
	if fresh.SyncEpoch() == first.Epoch {
		t.Fatal("new controller reused earlier epoch")
	}
}

func TestTrackedOldPassCannotConfirmNewQueuedRequest(t *testing.T) {
	c := New(schedFakeStore{}, &slowApplier{}, nil, "{}", "")
	// Hold the drain while an older pass runs, just as a real in-flight apply
	// does. Scheduling while it is running coalesces into the following pass.
	c.schedRunning = true
	var queued SyncTicket
	c.runTrackedRebuild(1, func() error { queued = c.ScheduleRebuildTracked(); return nil })
	old := c.SyncStatuses()[AllTarget]
	if old.State != "ok" || old.RequestRevision >= queued.Revision {
		t.Fatalf("older run claimed new save: %+v ticket=%+v", old, queued)
	}
	c.drain()
	latest := c.SyncStatuses()[AllTarget]
	if latest.RequestRevision != queued.Revision || latest.State != "ok" {
		t.Fatalf("queued request lost: %+v", latest)
	}
}

func TestTrackedFailureKeepsConcreteErrorAndRequestWatermark(t *testing.T) {
	c := New(schedFakeStore{}, &slowApplier{}, nil, "{}", "")
	c.runTrackedRebuild(12, func() error { return errors.New("entry A: SSH refused; landing B: core check failed") })
	got := c.SyncStatuses()[AllTarget]
	if got.State != "failed" || got.RequestRevision != 12 || got.Error != "entry A: SSH refused; landing B: core check failed" {
		t.Fatalf("lost failure: %+v", got)
	}
}

func TestConcurrentTrackedSchedulesCoalesceWithoutLosingNewestRequest(t *testing.T) {
	c := New(schedFakeStore{}, &slowApplier{}, nil, "{}", "")
	var wg sync.WaitGroup
	tickets := make(chan SyncTicket, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); tickets <- c.ScheduleRebuildTracked() }()
	}
	wg.Wait()
	close(tickets)
	waitDrained(t, c)
	seen := map[uint64]bool{}
	var latest uint64
	for ticket := range tickets {
		if ticket.Epoch != c.SyncEpoch() || ticket.Revision == 0 || seen[ticket.Revision] {
			t.Fatalf("duplicate/invalid ticket %+v", ticket)
		}
		seen[ticket.Revision] = true
		if ticket.Revision > latest {
			latest = ticket.Revision
		}
	}
	if status := c.SyncStatuses()[AllTarget]; status.State != "ok" || status.RequestRevision != latest {
		t.Fatalf("newest request not confirmed: %+v want %d", status, latest)
	}
}
