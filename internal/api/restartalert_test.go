package api

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/sbctl"
)

const (
	testWindow    = int64(30 * 60)
	testThreshold = 5
)

// TestRestartTrackerFiresOncePerEpisode pins the alert policy: quiet below the
// threshold, exactly one announcement when it is crossed, and silence
// afterwards for as long as the same episode continues.
//
// The "afterwards" half matters as much as the trigger: a node in a loop
// restarts every minute, so an alert that re-announced on every restart would
// send a message a minute to whoever is on the recipient list — which is how
// people end up muting the bot that was supposed to warn them.
func TestRestartTrackerFiresOncePerEpisode(t *testing.T) {
	tr := newRestartTracker()
	now := int64(1_700_000_000)

	for i := 1; i < testThreshold; i++ {
		fire, count := tr.record(7, "hk-01", now+int64(i)*60, testWindow, testThreshold)
		if fire {
			t.Fatalf("alerted after only %d restart(s)", count)
		}
	}
	fire, count := tr.record(7, "hk-01", now+int64(testThreshold)*60, testWindow, testThreshold)
	if !fire || count != testThreshold {
		t.Fatalf("threshold crossing did not alert (fire=%v count=%d)", fire, count)
	}
	for i := testThreshold + 1; i < testThreshold+5; i++ {
		if fire, _ := tr.record(7, "hk-01", now+int64(i)*60, testWindow, testThreshold); fire {
			t.Fatal("the same episode alerted twice")
		}
	}
}

// TestRestartTrackerForgetsOutsideTheWindow: restarts spread thinly over days
// are ordinary config changes, not a loop. Only what happened inside the window
// counts.
func TestRestartTrackerForgetsOutsideTheWindow(t *testing.T) {
	tr := newRestartTracker()
	now := int64(1_700_000_000)
	for i := 0; i < 20; i++ {
		// One restart per hour, with a 30-minute window: never two at once.
		if fire, count := tr.record(7, "hk-01", now+int64(i)*3600, testWindow, testThreshold); fire || count != 1 {
			t.Fatalf("hourly restarts counted as a loop (fire=%v count=%d)", fire, count)
		}
	}
}

// TestRestartTrackerRequiresExplicitRecovery pins the circuit semantics: quiet
// is what an open circuit looks like, so only a successful manual apply may
// clear it and arm a later episode.
func TestRestartTrackerRequiresExplicitRecovery(t *testing.T) {
	tr := newRestartTracker()
	now := int64(1_700_000_000)
	for i := 0; i < testThreshold; i++ {
		tr.record(7, "hk-01", now+int64(i)*60, testWindow, testThreshold)
	}

	quiet := now + int64(testThreshold)*60 + testWindow + 1
	tr.prune(quiet, testWindow)
	if !tr.alerted[7] {
		t.Fatal("quiet circuit was treated as recovered")
	}
	tr.recover(7)

	for i := 0; i < testThreshold-1; i++ {
		if fire, _ := tr.record(7, "hk-01", quiet+int64(i)*60, testWindow, testThreshold); fire {
			t.Fatal("relapse alerted before crossing the threshold again")
		}
	}
	if fire, _ := tr.record(7, "hk-01", quiet+int64(testThreshold)*60, testWindow, testThreshold); !fire {
		t.Fatal("a relapse after recovery never alerted")
	}
}

func TestRestartTrackerRetriesAfterDurableAlertFailure(t *testing.T) {
	tr := newRestartTracker()
	now := int64(1_700_000_000)
	for i := 0; i < testThreshold; i++ {
		tr.record(7, "hk-01", now+int64(i)*60, testWindow, testThreshold)
	}

	// runRestartWatch calls this when InsertAlert fails. The history must stay
	// intact so the immediately following restart sample crosses the threshold
	// again and retries the DB write/Telegram path.
	tr.unmarkAlerted(7)
	fire, count := tr.record(7, "hk-01", now+int64(testThreshold)*60, testWindow, testThreshold)
	if !fire || count != testThreshold+1 {
		t.Fatalf("failed durable alert was not retried: fire=%v count=%d", fire, count)
	}
}

func TestRestartCircuitTelegramMessagesDescribeTripAndRecovery(t *testing.T) {
	trip := renderRestartLoopAlert("hk-01", 5, 30)
	if !strings.Contains(trip, "已熔断") || !strings.Contains(trip, "流量统计") {
		t.Fatalf("trip message misses circuit impact: %q", trip)
	}
	recovered := renderRestartCircuitRecovery("hk-01")
	if !strings.Contains(recovered, "已恢复") || !strings.Contains(recovered, "周期性配置同步恢复") {
		t.Fatalf("recovery message misses restored state: %q", recovered)
	}
}

// TestRestartTrackerKeepsNodesApart: five nodes restarting once each is a
// config change reaching all of them, not five loops.
func TestRestartTrackerKeepsNodesApart(t *testing.T) {
	tr := newRestartTracker()
	now := int64(1_700_000_000)
	for id := int64(1); id <= 5; id++ {
		if fire, count := tr.record(id, "node", now, testWindow, testThreshold); fire || count != 1 {
			t.Fatalf("node %d: fire=%v count=%d", id, fire, count)
		}
	}
}

// TestNodeRestartedNeverBlocks is the load-bearing promise of this feature: it
// runs on the goroutine that deploys config to the nodes. Whatever is wrong
// downstream — watcher wedged, Telegram hung, queue full — reporting a restart
// must return immediately, because the alternative is an alerting side-channel
// holding up the thing it is supposed to be watching.
func TestNodeRestartedNeverBlocks(t *testing.T) {
	a := &API{restartCh: make(chan restartEvent, 2)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Far more than the queue holds, and nothing is draining it.
		for i := 0; i < 1000; i++ {
			a.NodeRestarted(1, "hk-01")
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reporting a restart blocked while the queue was full")
	}

	// A nil channel is the pre-wiring state (api.New before StartRestartWatch,
	// and every test that builds an API by hand); it must be a no-op, not a
	// panic or a permanent block.
	empty := &API{}
	empty.NodeRestarted(1, "hk-01")
	empty.sweepRestartAlerts()
}

func TestCircuitTripCarriesControllerDecisionWithoutReevaluation(t *testing.T) {
	a := &API{restartCh: make(chan restartEvent, 2)}
	a.NodeCircuitChanged(sbctl.RestartCircuitEvent{
		ServerID: 7, Name: "hk-01", Open: true, Count: 3, Window: 10 * time.Minute,
	})
	select {
	case ev := <-a.restartCh:
		if !ev.tripped || ev.recovered || ev.serverID != 7 || ev.count != 3 || ev.windowSec != 600 {
			t.Fatalf("trip event lost controller decision: %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("circuit trip was not queued")
	}
}

// TestParseChatIDs covers what an admin actually types into the extra-chats
// box, including the mistakes: a Chinese comma, stray spaces, a pasted @name
// that is not an id at all. One bad entry must not silence the rest.
func TestParseChatIDs(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []int64
	}{
		{"", nil},
		{"123", []int64{123}},
		{"123,456", []int64{123, 456}},
		{" 123 ，456\n-1001234567890 ", []int64{123, 456, -1001234567890}},
		{"@mychannel, 789", []int64{789}},
		{"abc", nil},
	} {
		if got := parseChatIDs(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("parseChatIDs(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
