package sbctl

import (
	"fmt"
	"sync"
	"time"
)

// RestartCircuitPolicy is shared with the API so the circuit breaker and the
// Telegram alert use one threshold. Disabling the restart alert also disables
// automatic circuit breaking.
type RestartCircuitPolicy struct {
	Enabled   bool
	Window    time.Duration
	Threshold int
}

// RestartCircuitEvent reports a circuit transition. Open transitions are
// accompanied by the regular restart event (which raises the alert); Close
// transitions are used to resolve it and send the recovery message.
type RestartCircuitEvent struct {
	ServerID int64
	Name     string
	Open     bool
	Count    int
	Window   time.Duration
}

type restartCircuit struct {
	mu   sync.Mutex
	hist map[int64][]time.Time
	open map[int64]bool
}

func newRestartCircuit() *restartCircuit {
	return &restartCircuit{hist: map[int64][]time.Time{}, open: map[int64]bool{}}
}

func (g *restartCircuit) record(serverID int64, now time.Time, p RestartCircuitPolicy) (opened bool, count int) {
	if !p.Enabled || p.Window <= 0 || p.Threshold <= 0 {
		return false, 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cutoff := now.Add(-p.Window)
	h := append(g.hist[serverID], now)
	kept := h[:0]
	for _, at := range h {
		if !at.Before(cutoff) {
			kept = append(kept, at)
		}
	}
	// The threshold is the only count that changes the decision. Bound history
	// so a bad node cannot grow memory forever while the panel stays up.
	max := p.Threshold * 2
	if max < 16 {
		max = 16
	}
	if len(kept) > max {
		kept = kept[len(kept)-max:]
	}
	g.hist[serverID] = kept
	count = len(kept)
	if count >= p.Threshold && !g.open[serverID] {
		g.open[serverID] = true
		return true, count
	}
	return false, count
}

func (g *restartCircuit) isOpen(serverID int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open[serverID]
}

func (g *restartCircuit) close(serverID int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	wasOpen := g.open[serverID]
	delete(g.open, serverID)
	delete(g.hist, serverID)
	return wasOpen
}

func circuitOpenError(p RestartCircuitPolicy) error {
	minutes := int64(p.Window / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Errorf("自动下发已熔断：%d 分钟内周期同步触发重启达到 %d 次；流量统计继续运行，请人工重新下发确认恢复", minutes, p.Threshold)
}
