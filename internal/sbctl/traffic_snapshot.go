package sbctl

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"qingzhou/internal/sbstats"
	"qingzhou/internal/store"
)

const cumulativeMeteringSetting = "traffic_cumulative_metering"

type trafficJournal interface {
	NextTrafficSequence() (int64, error)
	RecordTrafficPoll(store.TrafficPoll) (int, error)
	BeginTrafficReset(store.TrafficPoll) error
	TrafficCollectionState(int64) (store.TrafficMeteringQuality, string, error)
	GetSetting(string) (string, error)
	AcquireTrafficLease(int64, string) (bool, error)
	ReleaseTrafficLease(int64, string)
	RecordTrafficBoundaryGap(int64, string) error
}
type snapshotFetcher interface {
	QueryTraffic(context.Context, bool) (map[string]*sbstats.Traffic, error)
}

// Compatibility adapters can only reset. Once the durable mode is cumulative,
// they fail closed rather than silently executing an old destructive reader.
type legacySnapshotFetcher struct{ StatsFetcher }

func (f legacySnapshotFetcher) QueryTraffic(ctx context.Context, reset bool) (map[string]*sbstats.Traffic, error) {
	if !reset {
		return nil, fmt.Errorf("stats client does not support cumulative reads")
	}
	return f.QueryUserTraffic(ctx)
}

func (c *Controller) useTrafficSnapshots() bool {
	journal, ok := c.st.(trafficJournal)
	if !ok {
		return false
	}
	v, err := journal.GetSetting(cumulativeMeteringSetting)
	return err == nil && v == "true"
}
func trafficDeltas(m map[string]*sbstats.Traffic) map[string]store.UsageDelta {
	out := make(map[string]store.UsageDelta, len(m))
	for name, t := range m {
		out[name] = store.UsageDelta{Up: t.Up, Down: t.Down}
	}
	return out
}

var processEpochRE = regexp.MustCompile(`^[a-fA-F0-9-]{16,64}:[a-fA-F0-9]{32}$`)

func quoteEpochUnit(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// systemd InvocationID changes on every service invocation, unlike PID or a
// config hash. The boot ID guards reuse across host replacement/restarts.
func (c *Controller) trafficEpoch(ctx context.Context, sv *store.Server) (string, error) {
	unit := "sing-box"
	if sv != nil && sv.SystemdUnit != "" {
		unit = sv.SystemdUnit
	}
	if sv == nil {
		if env := os.Getenv("QZ_SINGBOX_UNIT"); env != "" {
			unit = env
		} else if journal, ok := c.st.(trafficJournal); ok {
			if v, e := journal.GetSetting("sb_systemd_unit"); e == nil && v != "" {
				unit = v
			}
		}
	}
	script := "set -eu; boot=$(cat /proc/sys/kernel/random/boot_id); invocation=$(systemctl show --property=InvocationID --value -- " + quoteEpochUnit(unit) + "); printf '%s:%s' \"$boot\" \"$invocation\""
	var out string
	var err error
	if sv != nil && (sv.Host == "" || !isLocalHostContext(ctx, sv.Host)) {
		if c.remoteMgr == nil {
			return "", fmt.Errorf("remote manager unavailable")
		}
		out, err = c.remoteMgr.RunCommand(ctx, SSHConfigFor(sv), script)
	} else {
		var b []byte
		command := exec.CommandContext(ctx, "sh", "-c", script)
		command.WaitDelay = 2 * time.Second
		b, err = command.Output()
		out = string(b)
	}
	out = strings.TrimSpace(out)
	if err != nil || !processEpochRE.MatchString(out) {
		return "", fmt.Errorf("cannot verify sing-box process epoch; counters were not reset")
	}
	return out, nil
}

// collectTrafficSnapshot performs one controlled reset→cumulative boundary on
// explicit opt-in. Afterwards it never resets a counter, including after a
// failed SSH read or panel restart. Each response is persisted independently.
func (c *Controller) collectTrafficSnapshot(ctx context.Context, serverID int64, sv *store.Server, fetch snapshotFetcher) (int, error) {

	journal := c.st.(trafficJournal)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	owner := uuid.NewString()
	acquired, err := journal.AcquireTrafficLease(serverID, owner)
	if err != nil {
		return 0, err
	}
	if !acquired {
		return 0, fmt.Errorf("another collector owns this node")
	}
	defer journal.ReleaseTrafficLease(serverID, owner)
	state, _, err := journal.TrafficCollectionState(serverID)
	if err != nil {
		return 0, err
	}
	// Every legacy reader shares this lease and rechecks the persisted mode.
	// This closes the late-reset race across panel processes during handover.
	if state.Mode != "cumulative" && !c.useTrafficSnapshots() {
		epoch := ""
		if needs, ok := c.st.(interface{ HasLegacyNumericCustomers() (bool, error) }); ok {
			if required, checkErr := needs.HasLegacyNumericCustomers(); checkErr != nil {
				return 0, checkErr
			} else if required {
				epoch, _ = c.trafficEpoch(ctx, sv)
			}
		}
		return c.collectResetTraffic(ctx, journal, serverID, sv, fetch, epoch, false)
	}
	before, err := c.trafficEpoch(ctx, sv)
	if err != nil {
		if state.Mode == "cumulative" {
			return 0, err
		}
		// Nodes without a verifiable systemd epoch keep their existing collector;
		// opting in must not silently stop quota metering on unsupported nodes.
		n, e := c.collectResetTraffic(ctx, journal, serverID, sv, fetch, "", false)
		c.recordTrafficFailure(serverID, "legacy")
		return n, e
	}
	if err = c.confirmCurrentRelayNamespace(ctx, serverID, sv, before); err != nil {
		c.recordTrafficFailure(serverID, "legacy_identity_unverified")
		return 0, err
	}
	applied := 0
	if state.Mode != "cumulative" {
		n, err := c.collectResetTraffic(ctx, journal, serverID, sv, fetch, before, true)
		applied += n
		if err != nil {
			return applied, err
		}
	}
	seq, err := journal.NextTrafficSequence()
	if err != nil {
		return applied, err
	}
	traffic, err := fetch.QueryTraffic(ctx, false)
	if err != nil {
		return applied, err
	}
	after, err := c.trafficEpoch(ctx, sv)
	if err != nil {
		return applied, err
	}
	if before != after {
		_ = journal.RecordTrafficBoundaryGap(serverID, "process_changed_during_read")
		return applied, fmt.Errorf("sing-box restarted during stats read; snapshot discarded")
	}
	poll := store.NewTrafficPoll(serverID, trafficDeltas(traffic))
	poll.Sequence = seq
	poll.Mode = "cumulative"
	poll.Epoch = after
	n, err := journal.RecordTrafficPoll(poll)
	return applied + n, err
}

func (c *Controller) collectResetTraffic(ctx context.Context, journal trafficJournal, serverID int64, sv *store.Server, fetch snapshotFetcher, epoch string, transition bool) (int, error) {
	if err := c.confirmCurrentRelayNamespace(ctx, serverID, sv, epoch); err != nil {
		c.recordTrafficFailure(serverID, "legacy_identity_unverified")
		return 0, err
	}
	seq, err := journal.NextTrafficSequence()
	if err != nil {
		return 0, err
	}
	poll := store.NewTrafficPoll(serverID, nil)
	poll.Sequence, poll.Epoch, poll.Transition = seq, epoch, transition
	if err = journal.BeginTrafficReset(poll); err != nil {
		return 0, fmt.Errorf("cannot persist reset intent; counters were not reset: %w", err)
	}
	traffic, err := fetch.QueryTraffic(ctx, true)
	if err != nil {
		return 0, fmt.Errorf("reset response uncertain; coverage warning retained: %w", err)
	}
	if epoch != "" {
		after, epochErr := c.trafficEpoch(ctx, sv)
		if epochErr != nil || after != epoch {
			return 0, fmt.Errorf("process changed during reset read; coverage warning retained")
		}
	}
	poll.Traffic = trafficDeltas(traffic)
	return journal.RecordTrafficPoll(poll)
}

// Legacy opt-out is intentionally rejected once cumulative cursors exist. A
// random switch back to reset would make the next old reader bill its already
// counted cumulative stock. The safe boundary must be an explicit operation.
func (c *Controller) cumulativeNodeRequiresCollector(serverID int64) bool {
	journal, ok := c.st.(trafficJournal)
	if !ok {
		return false
	}
	q, _, err := journal.TrafficCollectionState(serverID)
	return err == nil && q.Mode == "cumulative"
}
