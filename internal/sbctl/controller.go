// Package sbctl is the orchestration layer for 轻舟's native sing-box (B2). It
// ties the data layer, config generator, process manager and stats client
// together:
//
//   - Rebuild(): entitlement map → config → validate+reload (idempotent; call on
//     any change to users/inbounds/entitlement).
//   - CollectStats(): poll per-user traffic and accumulate it onto users; then
//     a Rebuild drops anyone who just went over quota.
//
// Dependencies are small interfaces so the controller is unit-testable with
// fakes; in production they are *store.Store, *sbproc.Manager and *sbstats.Client.
package sbctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"qingzhou/internal/sbproc"
	"qingzhou/internal/sbstats"
	"qingzhou/internal/sbver"
	"qingzhou/internal/singbox"
	"qingzhou/internal/sshctl"
	"qingzhou/internal/store"
)

// ConfigStore is the data layer the controller needs.
type ConfigStore interface {
	BuildUsersByTag(now int64) (map[string][]singbox.User, error)
	BuildSingboxConfig(base, v2rayListen string, usersByTag map[string][]singbox.User) ([]byte, error)
	BuildSingboxConfigForServer(serverID int64, base, v2rayListen string, usersByTag map[string][]singbox.User) ([]byte, error)
	AddUsageBatch(deltas map[string]store.UsageDelta) (int, error)
	ListServers() ([]*store.Server, error)
	GetServer(id int64) (*store.Server, error)
	// The capability probe already runs `sing-box version` on every node; these
	// let it keep what it learned instead of throwing the version away.
	SetNodeSingbox(serverID int64, info sbver.Info) error
	SetNodeSingboxError(serverID int64, msg string) error
}

// Applier validates and reloads a sing-box config (satisfied by *sbproc.Manager).
type Applier interface {
	Apply(config []byte) error
}

// StatsFetcher reads per-user traffic deltas (satisfied by *sbstats.Client).
type StatsFetcher interface {
	QueryUserTraffic(ctx context.Context) (map[string]*sbstats.Traffic, error)
}

// Controller orchestrates config regeneration and stats collection.
type Controller struct {
	st  ConfigStore
	mgr Applier

	// Cached timestamp of the local sing-box version probe; see version.go.
	localVerMu  sync.Mutex
	localVerAt  time.Time
	stats       StatsFetcher
	baseConfig  string
	v2rayListen string
	remoteMgr   *sshctl.RemoteManager // SSH manager for remote servers; nil if not configured

	mu sync.Mutex // serializes Rebuild

	// syncInterval is the period of the Run loop in nanoseconds, published for
	// callers that let a change ride that pass instead of forcing a rebuild. Zero
	// until Run starts.
	//
	// Deliberately NOT guarded by mu: mu is held for a whole rebuild, SSH pushes
	// to every server included, and a read of one int must not queue behind that
	// — the HTTP handler that quotes this interval would block for the length of
	// a rebuild and could outlive its own request timeout.
	syncInterval atomic.Int64

	// restartFailed tracks servers whose last local systemctl restart errored, so
	// applyLocal retries instead of short-circuiting on the already-swapped config
	// file. Keyed by server id; guarded separately from mu because applyLocal also
	// runs from the per-server goroutines RebuildServer fans out.
	restartMu     sync.Mutex
	restartFailed map[int64]bool

	// statsCap caches whether each remote node's sing-box has the v2ray_api
	// plugin. A node without it must not receive an experimental.v2ray_api block
	// (`sing-box check` would reject the config and the panel would stop being
	// able to deploy to that node at all), and must be skipped when collecting
	// stats so it doesn't log a refused connection every minute.
	capMu    sync.Mutex
	statsCap map[int64]statsProbe

	// Async rebuild scheduler: admin-triggered saves push config over SSH (up to
	// 90s per unreachable node), too slow to hold an HTTP response open. The API
	// schedules a rebuild and returns immediately; this coalesces bursts (one
	// in-flight pass + at most one queued follow-up) and records a per-target
	// SyncStatus the UI can poll. See schedule.go.
	schedMu       sync.Mutex
	schedRunning  bool
	pendingAll    bool
	pendingServer map[int64]bool
	syncStatus    map[int64]SyncStatus
	// statusSeq is a monotonic revision stamped onto every SyncStatus write. A
	// full rebuild reports each machine individually while it runs, so drain uses
	// the revision to tell "this machine already has its own result" from "this
	// machine was never reached" — timestamps can't, they share the same second.
	statusSeq uint64
}

// statsProbe is a cached capability answer plus when it was taken.
type statsProbe struct {
	ok bool
	at time.Time
}

// negativeProbeTTL re-probes nodes that answered "no". Upgrading a node's
// sing-box is exactly how an operator fixes an unmeterable node, and without
// an expiry the panel would keep ignoring that node until someone restarted the
// panel or hit 重建 on it — the fix would look like it did nothing.
// A positive answer is kept for the process lifetime: the plugin does not
// disappear from a binary on its own.
const negativeProbeTTL = 15 * time.Minute

// New builds a Controller. baseConfig is the log/dns/route/outbounds template
// JSON; v2rayListen is the gRPC stats listen address embedded into the config.
func New(st ConfigStore, mgr Applier, stats StatsFetcher, baseConfig, v2rayListen string) *Controller {
	return &Controller{
		st: st, mgr: mgr, stats: stats, baseConfig: baseConfig, v2rayListen: v2rayListen,
		restartFailed: map[int64]bool{},
		statsCap:      map[int64]statsProbe{},
		pendingServer: map[int64]bool{},
		syncStatus:    map[int64]SyncStatus{},
	}
}

// SetRemoteManager attaches the SSH remote manager for multi-server support.
func (c *Controller) SetRemoteManager(rm *sshctl.RemoteManager) {
	c.remoteMgr = rm
}

// isLocalHost reports whether host refers to the local machine (loopback or
// an IP assigned to a local interface). This lets the controller apply config
// directly instead of SSH-ing to itself when a server entry happens to point
// at the panel's own host.
func isLocalHost(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Bounded: this runs inside Rebuild, which holds c.mu, and before the
		// per-server goroutine fan-out — so an unbounded resolver lookup for one
		// server row stalls the whole rebuild and every admin-triggered
		// RebuildServer queued behind the mutex. The 90s per-server apply timeout
		// sits further down and does not cover this.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil || len(addrs) == 0 {
			return false
		}
		ip = net.ParseIP(addrs[0])
		if ip == nil {
			return false
		}
	}
	if ip.IsLoopback() {
		return true
	}
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range ifaces {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// resolveSingBoxBin picks the sing-box binary to use for a local server entry.
// If the server's configured SingBoxBin exists on disk, use it; otherwise
// auto-detect via sbproc.FindSingBoxBin().
func resolveSingBoxBin(configured string) (string, error) {
	if configured != "" {
		if fi, err := os.Stat(configured); err == nil && !fi.IsDir() {
			return configured, nil
		}
	}
	if b := sbproc.FindSingBoxBin(); b != "" {
		return b, nil
	}
	return "", fmt.Errorf("sing-box binary not found; set sing_box_bin or install sing-box in PATH")
}

// applyLocal writes, validates, and applies a sing-box config on the local
// machine for a server entry that is on the local host (avoiding SSH to self).
// It mirrors sbproc.Manager.Apply but uses the server entry's own config_path
// and systemd_unit instead of the global defaults.
func (c *Controller) applyLocal(sv *store.Server, cfg []byte) error {
	bin, err := resolveSingBoxBin(sv.SingBoxBin)
	if err != nil {
		return err
	}
	configPath := sv.ConfigPath
	if configPath == "" {
		configPath = "/etc/sing-box/config.json"
	}
	unit := sv.SystemdUnit
	if unit == "" {
		unit = "sing-box"
	}

	// No-op when config is byte-identical to what's already live — but not while a
	// restart for this server is still outstanding. The config is swapped before
	// systemctl runs, so matching bytes on disk prove the file landed, not that
	// sing-box came up on it; short-circuiting there would leave the unit down and
	// report success on every tick until an unrelated edit changed the config.
	c.restartMu.Lock()
	pending := c.restartFailed[sv.ID]
	c.restartMu.Unlock()
	if !pending {
		if cur, err := os.ReadFile(configPath); err == nil && bytes.Equal(cur, cfg) {
			return nil
		}
	}

	// Validate a scratch copy first, then install. The scratch file goes to the
	// temp dir rather than next to the live config: this machine is the panel's
	// own, whose unit mounts /etc read-only for the service, and validating is
	// not worth failing on a directory we have a way to write anyway (see
	// sbproc.WriteConfig, which escapes that sandbox for the install itself).
	tmp, err := os.CreateTemp("", "qz-sbcheck-*.json")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(cfg); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	// Validate with sing-box check.
	vctx, vcancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer vcancel()
	vout, verr := exec.CommandContext(vctx, bin, "check", "-c", tmpPath).CombinedOutput()
	if verr != nil {
		return fmt.Errorf("sing-box check failed: %v: %s", verr, vout)
	}

	if err := sbproc.WriteConfig(configPath, cfg); err != nil {
		return fmt.Errorf("install config: %w", err)
	}

	// Restart the systemd unit.
	rctx, rcancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer rcancel()
	rout, rerr := exec.CommandContext(rctx, "systemctl", "restart", unit).CombinedOutput()
	c.restartMu.Lock()
	if rerr != nil {
		c.restartFailed[sv.ID] = true
	} else {
		delete(c.restartFailed, sv.ID)
	}
	c.restartMu.Unlock()
	if rerr != nil {
		return fmt.Errorf("systemctl restart %s: %v: %s", unit, rerr, rout)
	}
	return nil
}

// statsListenFor returns the v2ray_api listen address to bake into a server's
// config, or "" to omit the block entirely.
//
// The local instance always gets it. A remote node gets it only if its sing-box
// actually has the plugin: probing costs one SSH command, and the result is
// cached, because guessing wrong in the permissive direction breaks config
// deployment to that node permanently.
func (c *Controller) statsListenFor(sv *store.Server) string {
	if sv.ID == 0 || isLocalHost(sv.Host) {
		return c.v2rayListen
	}
	listen := sv.V2rayListen
	if listen == "" {
		return "" // no address configured for this node — nothing to expose
	}
	if !c.statsSupported(sv) {
		return ""
	}
	return listen
}

// statsSupported reports (and caches) whether a remote node can serve the stats
// API. A probe failure is cached as "no" for this cycle rather than retried in
// line; the next Rebuild re-probes, so a node that was merely unreachable
// recovers on its own.
func (c *Controller) statsSupported(sv *store.Server) bool {
	c.capMu.Lock()
	if p, seen := c.statsCap[sv.ID]; seen && (p.ok || time.Since(p.at) < negativeProbeTTL) {
		c.capMu.Unlock()
		return p.ok
	}
	c.capMu.Unlock()

	if c.remoteMgr == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ok, version, err := c.remoteMgr.SupportsStatsAPI(ctx, serverConfigFor(sv))
	// Record what the probe saw either way. This is the panel's only window onto
	// which sing-box a node actually ended up with after the one-line installer,
	// and an operator otherwise has to SSH in to find out.
	if err != nil {
		_ = c.st.SetNodeSingboxError(sv.ID, err.Error())
	} else {
		_ = c.st.SetNodeSingbox(sv.ID, sbver.Parse(version))
	}
	if err != nil {
		log.Printf("sbctl: server %d (%s) stats capability probe failed: %v", sv.ID, sv.Name, err)
		ok = false
	} else if !ok {
		// Include what the probe actually saw: an operator otherwise cannot tell a
		// build that genuinely lacks the plugin from a probe that resolved the
		// wrong binary (or none at all, which prints "unknown").
		log.Printf("sbctl: server %d (%s) sing-box has no v2ray_api plugin — traffic on this node will NOT be metered. `sing-box version` said: %s",
			sv.ID, sv.Name, strings.Join(strings.Fields(version), " "))
	}
	c.capMu.Lock()
	c.statsCap[sv.ID] = statsProbe{ok: ok, at: time.Now()}
	c.capMu.Unlock()
	return ok
}

// forgetStatsCap drops a cached probe result so the next Rebuild re-probes —
// used after a node is edited, since its binary or path may have changed.
func (c *Controller) forgetStatsCap(serverID int64) {
	c.capMu.Lock()
	delete(c.statsCap, serverID)
	c.capMu.Unlock()
}

// Rebuild regenerates the sing-box config from the current entitlement and
// applies it (validate + reload). Safe to call on every change; serialized.
// When multi-server is configured, it iterates over all enabled remote servers
// in addition to the local instance.
func (c *Controller) Rebuild() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Build the entitlement map once (shared across all servers).
	byTag, err := c.st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		return err
	}

	// Apply to local server (server_id=0, the legacy path).
	var lastErr error
	// record keeps a per-machine outcome alongside the aggregate lastErr. Without
	// it a full rebuild reports one "下发失败" for the whole pass, and the admin
	// UI can only say that *something* failed — not which machine, nor why.
	record := func(id int64, err error) {
		if err != nil {
			c.setStatus(id, "failed", err.Error())
			return
		}
		c.setStatus(id, "ok", "")
	}
	if cfg, err := c.st.BuildSingboxConfig(c.baseConfig, c.v2rayListen, byTag); err != nil {
		lastErr = fmt.Errorf("local build config: %w", err)
		log.Printf("sbctl: local rebuild error: %v", err)
		record(0, lastErr)
	} else if err := c.mgr.Apply(cfg); err != nil {
		lastErr = fmt.Errorf("local apply: %w", err)
		log.Printf("sbctl: local apply error: %v", err)
		record(0, lastErr)
	} else {
		record(0, nil)
	}

	// Apply to each enabled server. Servers whose host is the local machine
	// are applied directly (no SSH); remote servers are applied via SSH
	// concurrently — an unreachable server must not stall the others.
	servers, err := c.st.ListServers()
	if err != nil {
		log.Printf("sbctl: list servers error: %v", err)
		return lastErr
	}
	var wg sync.WaitGroup
	var remoteMu sync.Mutex
	setErr := func(err error) {
		remoteMu.Lock()
		lastErr = err
		remoteMu.Unlock()
	}
	for _, sv := range servers {
		if !sv.Enabled {
			continue
		}
		cfg, err := c.st.BuildSingboxConfigForServer(sv.ID, c.baseConfig, c.statsListenFor(sv), byTag)
		if err != nil {
			log.Printf("sbctl: server %d (%s) build config error: %v", sv.ID, sv.Name, err)
			e := fmt.Errorf("生成配置失败: %w", err)
			setErr(fmt.Errorf("server %d build config: %w", sv.ID, err))
			record(sv.ID, e)
			continue
		}
		// Local server entry — apply directly without SSH.
		if isLocalHost(sv.Host) {
			if err := c.applyLocal(sv, cfg); err != nil {
				log.Printf("sbctl: server %d (%s) local apply error: %v", sv.ID, sv.Name, err)
				setErr(fmt.Errorf("server %d apply: %w", sv.ID, err))
				record(sv.ID, fmt.Errorf("本机下发失败: %w", err))
			} else {
				record(sv.ID, nil)
			}
			continue
		}
		// Remote server — apply via SSH.
		if c.remoteMgr == nil {
			setErr(fmt.Errorf("server %d apply: remote manager not configured", sv.ID))
			record(sv.ID, fmt.Errorf("未配置远程管理器，无法通过 SSH 下发"))
			continue
		}
		serverCfg := serverConfigFor(sv)
		wg.Add(1)
		go func(sv *store.Server, serverCfg *sshctl.ServerConfig, cfg []byte) {
			defer wg.Done()
			// Bound the apply so one unreachable / half-open node can't block on
			// session.Wait() indefinitely and wedge Rebuild (which holds c.mu).
			applyCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if err := c.remoteMgr.ApplyConfig(applyCtx, serverCfg, cfg); err != nil {
				log.Printf("sbctl: server %d (%s) apply error: %v", sv.ID, sv.Name, err)
				setErr(fmt.Errorf("server %d apply: %w", sv.ID, err))
				record(sv.ID, fmt.Errorf("SSH 下发失败: %w", err))
				return
			}
			record(sv.ID, nil)
		}(sv, serverCfg, cfg)
	}
	wg.Wait()

	return lastErr
}

// RebuildServer regenerates and applies the sing-box config for a single
// server. serverID=0 means the local panel server; any other value means a
// server entry — which may be on the local machine (applied directly) or a
// remote host (applied via SSH).
func (c *Controller) RebuildServer(serverID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// This is the admin-triggered path, reached right after a server is edited or
	// its sing-box reinstalled — so re-probe rather than trusting a cached answer
	// about a binary that may have just changed.
	c.forgetStatsCap(serverID)

	byTag, err := c.st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		return err
	}

	if serverID == 0 {
		// Local server (legacy server_id=0).
		cfg, err := c.st.BuildSingboxConfig(c.baseConfig, c.v2rayListen, byTag)
		if err != nil {
			return fmt.Errorf("local build config: %w", err)
		}
		if err := c.mgr.Apply(cfg); err != nil {
			return fmt.Errorf("local apply: %w", err)
		}
		return nil
	}

	// Server entry (server_id > 0) — could be local or remote.
	sv, err := c.st.GetServer(serverID)
	if err != nil {
		return fmt.Errorf("get server %d: %w", serverID, err)
	}
	// GetServer returns (nil, nil) for a missing row. Guard before dereferencing
	// sv — otherwise an inbound whose server_id points to a deleted server panics
	// the whole request (nil pointer) instead of failing cleanly.
	if sv == nil {
		return fmt.Errorf("服务器 %d 不存在（可能已被删除）", serverID)
	}
	cfg, err := c.st.BuildSingboxConfigForServer(sv.ID, c.baseConfig, c.statsListenFor(sv), byTag)
	if err != nil {
		return fmt.Errorf("server %d build config: %w", sv.ID, err)
	}

	// If the server is on the local machine, apply directly (no SSH).
	if isLocalHost(sv.Host) {
		if err := c.applyLocal(sv, cfg); err != nil {
			return fmt.Errorf("server %d apply: %w", sv.ID, err)
		}
		return nil
	}

	// Remote server — apply via SSH.
	if c.remoteMgr == nil {
		return fmt.Errorf("remote manager not configured")
	}
	serverCfg := serverConfigFor(sv)
	applyCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := c.remoteMgr.ApplyConfig(applyCtx, serverCfg, cfg); err != nil {
		return fmt.Errorf("server %d apply: %w", sv.ID, err)
	}
	return nil
}

// serverConfigFor projects a stored server row onto the SSH config the remote
// manager takes. Single definition so a newly-stored field can't reach one call
// site and silently miss another.
func serverConfigFor(sv *store.Server) *sshctl.ServerConfig {
	return &sshctl.ServerConfig{
		ID: sv.ID, Name: sv.Name, Host: sv.Host, Port: sv.Port,
		SSHUser: sv.SSHUser, SSHKey: sv.SSHKey, SSHKeyPass: sv.SSHKeyPass,
		SSHPassword: sv.SSHPassword, ConfigPath: sv.ConfigPath,
		SystemdUnit: sv.SystemdUnit, SingBoxBin: sv.SingBoxBin,
		V2rayListen: sv.V2rayListen, HostKey: sv.HostKey,
	}
}

// CollectStats polls per-user traffic deltas — from the local sing-box and from
// every enabled remote server — and accumulates them onto users. Returns the
// number of users whose usage changed.
//
// Remote servers each run their own sing-box with their own counters. Polling
// only the local one (which is all this did) meant traffic through any remote
// node was never metered, so quota enforcement never fired there: effectively
// unlimited traffic on every remote node.
//
// A per-identity sum is correct across servers: bucket client_names are globally
// unique, and a user reachable on two nodes should be charged for both.
func (c *Controller) CollectStats(ctx context.Context) (int, error) {
	deltas := map[string]store.UsageDelta{}
	add := func(m map[string]*sbstats.Traffic) {
		for name, t := range m {
			if t.Up == 0 && t.Down == 0 {
				continue
			}
			d := deltas[name]
			d.Up += t.Up
			d.Down += t.Down
			deltas[name] = d
		}
	}

	var errs []error
	m, err := c.stats.QueryUserTraffic(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("local stats: %w", err))
	} else {
		add(m)
	}
	for _, rm := range c.remoteStats(ctx) {
		if rm.err != nil {
			errs = append(errs, rm.err)
			continue
		}
		add(rm.traffic)
	}

	// Commit whatever was collected even if some server failed. Each successful
	// poll used reset=true, so its counters are already zeroed on that node —
	// bailing out here would throw that traffic away permanently.
	//
	// Apply the whole poll in one transaction (one WAL write-lock acquisition
	// instead of one per identity). AddUsageBatch isolates each identity in a
	// savepoint, so one bad delta doesn't discard the rest.
	n, err := c.st.AddUsageBatch(deltas)
	if err != nil {
		errs = append(errs, err)
	}
	return n, errors.Join(errs...)
}

type remoteResult struct {
	traffic map[string]*sbstats.Traffic
	err     error
}

// remoteStats polls every enabled remote server's stats API through an SSH
// tunnel, concurrently. A server that fails yields an error rather than aborting
// the others — its counters were not reset, so its traffic is picked up next poll.
func (c *Controller) remoteStats(ctx context.Context) []remoteResult {
	if c.remoteMgr == nil {
		return nil
	}
	servers, err := c.st.ListServers()
	if err != nil {
		return []remoteResult{{err: fmt.Errorf("list servers for stats: %w", err)}}
	}
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		out []remoteResult
	)
	for _, sv := range servers {
		if !sv.Enabled || isLocalHost(sv.Host) {
			continue // the local instance is polled directly
		}
		// Only poll nodes whose config actually carries the stats API. Polling a
		// node without the v2ray_api plugin just logs a refused connection every
		// interval; statsListenFor already decided to omit the block for it.
		listen := c.statsListenFor(sv)
		if listen == "" {
			continue
		}
		wg.Add(1)
		go func(sv *store.Server, listen string) {
			defer wg.Done()
			cfg := serverConfigFor(sv)
			sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			client := sbstats.NewWithDialer(listen, func(dctx context.Context, _, addr string) (net.Conn, error) {
				return c.remoteMgr.DialTunnel(dctx, cfg, addr)
			})
			// This client is built fresh every poll, so it must be released:
			// each tunnel it dials carries its own *ssh.Client, which stays
			// alive — as a live sshd session on the node — until the connection
			// is closed. One poll per minute per server otherwise piles up
			// sshd processes on the node until it runs out of memory.
			defer client.Close()
			t, err := client.QueryUserTraffic(sctx)
			if err != nil {
				err = fmt.Errorf("server %d (%s) stats: %w", sv.ID, sv.Name, err)
			}
			mu.Lock()
			out = append(out, remoteResult{traffic: t, err: err})
			mu.Unlock()
		}(sv, listen)
	}
	wg.Wait()
	return out
}

// SyncInterval is how often the periodic pass applies pending changes to the
// nodes, i.e. the worst-case delay for a change that doesn't force its own
// rebuild. Returns a minute — the Run default — before Run has started.
func (c *Controller) SyncInterval() time.Duration {
	if d := c.syncInterval.Load(); d > 0 {
		return time.Duration(d)
	}
	return time.Minute
}

// Run drives the periodic loop: every interval it collects stats then rebuilds
// (so over-quota users are dropped promptly). Blocks until ctx is cancelled.
// errFn (optional) receives non-fatal errors.
func (c *Controller) Run(ctx context.Context, interval time.Duration, errFn func(error)) {
	report := func(err error) {
		if err != nil && errFn != nil {
			errFn(err)
		}
	}
	// time.NewTicker panics on a non-positive interval; a bad QZ_SINGBOX_STATS_INTERVAL
	// (e.g. "0s") must not crash-loop the process. Clamp to a sane minimum here so
	// every caller is protected regardless of how the interval was derived.
	if interval <= 0 {
		interval = time.Minute
	}
	// Publish the effective interval: it is how long a change that isn't pushed
	// explicitly (a node-credential rotation, which rides this pass rather than
	// forcing its own restart) can take to reach the nodes, and the UI quotes it.
	c.syncInterval.Store(int64(interval))
	report(c.Rebuild()) // apply current state on startup
	// The remote nodes get their version recorded by the capability probe inside
	// Rebuild; the local machine has no such path, so it is probed here.
	c.refreshLocalVersion(false)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := c.CollectStats(ctx); err != nil {
				report(err)
			}
			report(c.Rebuild())
			c.refreshLocalVersion(false)
		}
	}
}
