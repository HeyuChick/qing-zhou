package api

import (
	"reflect"
	"testing"
	"time"
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

// TestRestartTrackerClosesAndReopensEpisodes covers recovery: once a node has
// been quiet for a full window the alert is closed, and a relapse is a new
// episode that announces itself again rather than being swallowed as "already
// alerted".
func TestRestartTrackerClosesAndReopensEpisodes(t *testing.T) {
	tr := newRestartTracker()
	now := int64(1_700_000_000)
	for i := 0; i < testThreshold; i++ {
		tr.record(7, "hk-01", now+int64(i)*60, testWindow, testThreshold)
	}

	// Still restarting — nothing to close.
	if got := tr.idle(now+int64(testThreshold)*60, testWindow); len(got) != 0 {
		t.Fatalf("closed an episode that is still going: %v", got)
	}

	quiet := now + int64(testThreshold)*60 + testWindow + 1
	if got := tr.idle(quiet, testWindow); !reflect.DeepEqual(got, []int64{7}) {
		t.Fatalf("a node quiet for a full window was not closed: %v", got)
	}
	// Closing twice must not produce a second resolve.
	if got := tr.idle(quiet+1, testWindow); len(got) != 0 {
		t.Fatalf("closed the same episode twice: %v", got)
	}

	for i := 0; i < testThreshold-1; i++ {
		if fire, _ := tr.record(7, "hk-01", quiet+int64(i)*60, testWindow, testThreshold); fire {
			t.Fatal("relapse alerted before crossing the threshold again")
		}
	}
	if fire, _ := tr.record(7, "hk-01", quiet+int64(testThreshold)*60, testWindow, testThreshold); !fire {
		t.Fatal("a relapse after recovery never alerted")
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
