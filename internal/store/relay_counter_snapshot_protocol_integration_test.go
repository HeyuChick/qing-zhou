package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Use panel IDs in diagnostics, never client credentials or derived wire keys.
type relayFixtureCounterID struct {
	Hop   int
	Owner int64
}

type relayFixtureCounterTarget struct {
	Before  UsageDelta
	Minimum UsageDelta // completed application payload, not a substituted raw count
	Frozen  bool
}

type relayFixtureCounterSample map[relayFixtureCounterID]UsageDelta

type relayFixtureCounterSettler struct {
	targets     map[relayFixtureCounterID]relayFixtureCounterTarget
	first, last relayFixtureCounterSample
	stableSince time.Time
	deadline    time.Time
	stableFor   time.Duration
	samples     int
}

func cloneRelayCounterSample(sample relayFixtureCounterSample) relayFixtureCounterSample {
	copy := make(relayFixtureCounterSample, len(sample))
	for key, value := range sample {
		copy[key] = value
	}
	return copy
}

// Bytes can reach the receiving application before the sending core returns
// from WriteBuffer and executes its atomic stats increment. Observe only waits
// for that read-side visibility boundary. It never generates or retries traffic,
// adjusts a counter, or accepts an unrelated owner's intermediate movement.
func (s *relayFixtureCounterSettler) observe(now time.Time, sample relayFixtureCounterSample) (bool, error) {
	s.samples++
	if s.first == nil {
		s.first = cloneRelayCounterSample(sample)
	}
	previous := s.last
	s.last = cloneRelayCounterSample(sample)
	ready, changed := true, previous == nil
	for key, target := range s.targets {
		got, ok := sample[key]
		if !ok {
			return false, fmt.Errorf("missing counter hop=%d owner=%d", key.Hop, key.Owner)
		}
		if target.Frozen && got != target.Before {
			return false, fmt.Errorf("unrelated counter moved hop=%d owner=%d before=%+v got=%+v", key.Hop, key.Owner, target.Before, got)
		}
		before := target.Before
		if previous != nil {
			before = previous[key]
		}
		if got.Up < before.Up || got.Down < before.Down {
			return false, fmt.Errorf("counter regressed hop=%d owner=%d before=%+v got=%+v", key.Hop, key.Owner, before, got)
		}
		if got.Up < target.Minimum.Up || got.Down < target.Minimum.Down {
			ready = false
		}
		if previous != nil && got != previous[key] {
			changed = true
		}
	}
	if !now.Before(s.deadline) {
		return false, fmt.Errorf("completed-payload counters did not become stable before deadline")
	}
	if !ready {
		s.stableSince = time.Time{}
		return false, nil
	}
	if changed || s.stableSince.IsZero() {
		s.stableSince = now
		return false, nil
	}
	return now.Sub(s.stableSince) >= s.stableFor, nil
}

func TestRelayCounterSnapshotWaitsForPayloadAndStability(t *testing.T) {
	start := time.Unix(100, 0)
	a, b := relayFixtureCounterID{0, 1}, relayFixtureCounterID{0, 2}
	oldA := UsageDelta{Up: 477, Down: 65675}
	s := relayFixtureCounterSettler{
		targets: map[relayFixtureCounterID]relayFixtureCounterTarget{
			a: {Before: oldA, Minimum: oldA, Frozen: true},
			b: {Minimum: UsageDelta{Up: 512, Down: 131072}},
		},
		deadline: start.Add(2 * time.Second), stableFor: 50 * time.Millisecond,
	}
	for _, step := range []struct {
		after time.Duration
		down  int64
		ready bool
	}{
		{0, 118442, false},
		{10 * time.Millisecond, 131211, false},
		{40 * time.Millisecond, 131211, false},
		{50 * time.Millisecond, 131212, false}, // trailing bytes reset stability
		{99 * time.Millisecond, 131212, false},
		{100 * time.Millisecond, 131212, true},
	} {
		got, err := s.observe(start.Add(step.after), relayFixtureCounterSample{a: oldA, b: {Up: 734, Down: step.down}})
		if err != nil || got != step.ready {
			t.Fatalf("after=%s settled=%t want=%t err=%v", step.after, got, step.ready, err)
		}
	}
	if s.first[b].Down != 118442 || s.last[b].Down != 131212 {
		t.Fatal("diagnostics lost the first or last actual counter sample")
	}
}

func TestRelayCounterSnapshotNeverHidesCrossUserMovement(t *testing.T) {
	start := time.Unix(100, 0)
	a, b := relayFixtureCounterID{0, 1}, relayFixtureCounterID{0, 2}
	before := UsageDelta{Up: 10, Down: 100}
	s := relayFixtureCounterSettler{
		targets: map[relayFixtureCounterID]relayFixtureCounterTarget{
			a: {Before: before, Minimum: before, Frozen: true},
			b: {Minimum: UsageDelta{Up: 20, Down: 200}},
		},
		deadline: start.Add(time.Second), stableFor: 50 * time.Millisecond,
	}
	// B is still below its floor. Waiting for B must not postpone or hide A's
	// movement, even if a hypothetical later sample returned to the baseline.
	_, err := s.observe(start, relayFixtureCounterSample{a: {Up: 10, Down: 101}, b: {Up: 1, Down: 1}})
	if err == nil || !strings.Contains(err.Error(), "unrelated counter moved") {
		t.Fatalf("cross-user movement was not rejected immediately: %v", err)
	}
}

func TestRelayCounterSnapshotRejectsPermanentUndercountAndRegression(t *testing.T) {
	start := time.Unix(100, 0)
	key := relayFixtureCounterID{1, 2}
	for _, regression := range []bool{false, true} {
		s := relayFixtureCounterSettler{
			targets:  map[relayFixtureCounterID]relayFixtureCounterTarget{key: {Minimum: UsageDelta{Up: 20, Down: 200}}},
			deadline: start.Add(time.Second), stableFor: 50 * time.Millisecond,
		}
		if ready, err := s.observe(start, relayFixtureCounterSample{key: {Up: 20, Down: 150}}); ready || err != nil {
			t.Fatalf("undercount prematurely accepted: ready=%t err=%v", ready, err)
		}
		now, down, want := start.Add(time.Second), int64(150), "before deadline"
		if regression {
			now, down, want = start.Add(time.Millisecond), 149, "counter regressed"
		}
		if _, err := s.observe(now, relayFixtureCounterSample{key: {Up: 20, Down: down}}); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("regression=%t error=%v, want %q", regression, err, want)
		}
	}
}
