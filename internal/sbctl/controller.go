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
	"path/filepath"
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

	// restartObserver is told about every sing-box restart caused by the PERIODIC
	// sync pass — the ones nobody asked for. Restarts from an admin edit are
	// deliberate and are not reported, so a watcher downstream can treat what it
	// receives as unexplained by definition. nil disables the reporting.
	//
	// Written once during startup wiring, before Run and therefore before any
	// rebuild goroutine exists, so it needs no lock. Not a field to start
	// swapping at runtime.
	restartObserver func(serverID int64, name string)

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

// SetRestartObserver registers a callback for restarts caused by the periodic
// pass. It runs on the rebuild goroutine, so it must not block: the alert
// watcher behind it does its own bookkeeping in memory and hands anything
// slower (a DB write, a Telegram push) to its own worker.
func (c *Controller) SetRestartObserver(fn func(serverID int64, name string)) {
	c.restartObserver = fn
}

// notifyRestart reports one restart, if anyone is listening and this pass was
// the periodic one.
func (c *Controller) notifyRestart(periodic bool, serverID int64, name string) {
	if !periodic || c.restartObserver == nil {
		return
	}
	c.restartObserver(serverID, name)
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

// applyPanel installs the panel machine's own config, reporting whether
// sing-box was reloaded. Applier itself only returns an error (the test fakes
// implement just that), so the richer answer is taken when the real manager is
// behind the interface.
func (c *Controller) applyPanel(cfg []byte) (bool, error) {
	if r, ok := c.mgr.(interface {
		ApplyChanged([]byte) (bool, error)
	}); ok {
		return r.ApplyChanged(cfg)
	}
	return false, c.mgr.Apply(cfg)
}

// localConfigPath is the file the panel's own sing-box config is installed at,
// or "" when the applier does not expose one (the fakes in tests).
func (c *Controller) localConfigPath() string {
	if p, ok := c.mgr.(interface{ ConfigPath() string }); ok {
		return p.ConfigPath()
	}
	return ""
}

// serverConfigPath is where a server row's config is installed, applying the
// same default the row itself leaves implicit.
func serverConfigPath(sv *store.Server) string {
	if sv.ConfigPath == "" {
		return "/etc/sing-box/config.json"
	}
	return sv.ConfigPath
}

// panelPathConflict rejects a server row that would fight the panel over one
// file.
//
// Rebuild always installs the panel's own config (server_id 0, every inbound)
// at the applier's path. A row that resolves to this same machine and points at
// that same file is a second writer with different content: each pass finds the
// other's bytes, calls that a change, rewrites and restarts sing-box. Both
// applies report success, the config on disk is valid either way, and nothing
// in the logs distinguishes it from ordinary deploys — while every connection on
// the box is cut twice a minute, forever.
//
// Identical bytes are fine and are the tidy shape of this setup (every inbound
// moved onto the row, nothing left at server_id 0), so this compares content
// rather than banning the path outright.
func panelPathConflict(panelPath string, sv *store.Server, serverCfg, panelCfg []byte) error {
	if panelPath == "" || panelCfg == nil {
		return nil
	}
	if filepath.Clean(serverConfigPath(sv)) != filepath.Clean(panelPath) {
		return nil
	}
	if bytes.Equal(serverCfg, panelCfg) {
		return nil
	}
	return fmt.Errorf("配置路径 %s 和面板本机（server_id=0）是同一个文件，但两边生成的配置不一样："+
		"下发后会互相覆盖，sing-box 每轮同步都要重启一次，这台机器上的连接会反复中断。"+
		"请把该机器的入站全部挂到这个节点下，或者给这个节点换一个 config_path", serverConfigPath(sv))
}

// applyLocal writes, validates, and applies a sing-box config on the local
// machine for a server entry that is on the local host (avoiding SSH to self).
// It mirrors sbproc.Manager.Apply but uses the server entry's own config_path
// and systemd_unit instead of the global defaults.
func (c *Controller) applyLocal(sv *store.Server, cfg []byte) (bool, error) {
	bin, err := resolveSingBoxBin(sv.SingBoxBin)
	if err != nil {
		return false, err
	}
	configPath := serverConfigPath(sv)
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
			return false, nil
		}
	}

	// Validate a scratch copy first, then install. The scratch file goes to the
	// temp dir rather than next to the live config: this machine is the panel's
	// own, whose unit mounts /etc read-only for the service, and validating is
	// not worth failing on a directory we have a way to write anyway (see
	// sbproc.WriteConfig, which escapes that sandbox for the install itself).
	tmp, err := os.CreateTemp("", "qz-sbcheck-*.json")
	if err != nil {
		return false, fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(cfg); err != nil {
		tmp.Close()
		return false, err
	}
	tmp.Close()

	// Validate with sing-box check.
	vctx, vcancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer vcancel()
	vout, verr := exec.CommandContext(vctx, bin, "check", "-c", tmpPath).CombinedOutput()
	if verr != nil {
		return false, fmt.Errorf("sing-box check failed: %v: %s", verr, vout)
	}

	if err := sbproc.WriteConfig(configPath, cfg); err != nil {
		return false, fmt.Errorf("install config: %w", err)
	}

	// Same reason as the SSH path: a restart is the one thing users feel, so it
	// leaves a trace even when it succeeds.
	log.Printf("sbctl: server %d (%s) 配置有变化，已写入 %s 并重启 %s（该节点的连接会断一次）", sv.ID, sv.Name, configPath, unit)

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
	// Restarted either way from here on: the config was swapped and systemctl was
	// told to go, so the connections on this machine are gone whether or not the
	// unit came back up.
	if rerr != nil {
		return true, fmt.Errorf("systemctl restart %s: %v: %s", unit, rerr, rout)
	}
	return true, nil
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
	ok, version, err := c.remoteMgr.SupportsStatsAPI(ctx, SSHConfigFor(sv))
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

// invalidateRemoteCaches forces the next operation on a server to rediscover
// both its capabilities and its sing-box path. The RemoteManager is long-lived;
// clearing a short-lived API manager after an upgrade does not touch this cache.
func (c *Controller) invalidateRemoteCaches(serverID int64) {
	c.forgetStatsCap(serverID)
	if c.remoteMgr != nil {
		c.remoteMgr.ForgetSingBoxBin(serverID)
	}
}

// Rebuild regenerates the sing-box config from the current entitlement and
// applies it (validate + reload). Safe to call on every change; serialized.
// When multi-server is configured, it iterates over all enabled remote servers
// in addition to the local instance.
func (c *Controller) Rebuild() error { return c.rebuild(false) }

// rebuildPeriodic is the timer-driven pass. Restarts it causes are reported to
// the restart observer; restarts from an admin's own edit are not, because a
// node restarting right after someone changed it is the system working.
func (c *Controller) rebuildPeriodic() error { return c.rebuild(true) }

func (c *Controller) rebuild(periodic bool) error {
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
	// Kept for the conflict check below: a server row that lands on this machine
	// and shares this file is a second writer, and the two only coexist while
	// they generate the same bytes.
	var panelCfg []byte
	if cfg, err := c.st.BuildSingboxConfig(c.baseConfig, c.v2rayListen, byTag); err != nil {
		lastErr = fmt.Errorf("local build config: %w", err)
		log.Printf("sbctl: local rebuild error: %v", err)
		record(0, lastErr)
	} else {
		panelCfg = cfg
		restarted, err := c.applyPanel(cfg)
		if restarted {
			c.notifyRestart(periodic, store.LocalNodeID, store.LocalNodeName)
		}
		if err != nil {
			lastErr = fmt.Errorf("local apply: %w", err)
			log.Printf("sbctl: local apply error: %v", err)
			record(0, lastErr)
		} else {
			record(0, nil)
		}
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
			if err := panelPathConflict(c.localConfigPath(), sv, cfg, panelCfg); err != nil {
				log.Printf("sbctl: server %d (%s) %v", sv.ID, sv.Name, err)
				setErr(fmt.Errorf("server %d apply: %w", sv.ID, err))
				record(sv.ID, err)
				continue
			}
			restarted, err := c.applyLocal(sv, cfg)
			if restarted {
				c.notifyRestart(periodic, sv.ID, sv.Name)
			}
			if err != nil {
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
		serverCfg := SSHConfigFor(sv)
		wg.Add(1)
		go func(sv *store.Server, serverCfg *sshctl.ServerConfig, cfg []byte) {
			defer wg.Done()
			// Bound the apply so one unreachable / half-open node can't block on
			// session.Wait() indefinitely and wedge Rebuild (which holds c.mu).
			applyCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			restarted, err := c.remoteMgr.ApplyConfig(applyCtx, serverCfg, cfg)
			if restarted {
				// Every connection on this node was just cut. Say so, once per
				// occurrence: a line that shows up on every pass is a restart loop,
				// and a silent one is how a node ends up cycling for a day before
				// anyone connects the disconnects to the panel.
				log.Printf("sbctl: server %d (%s) 配置有变化，已下发并重启 sing-box（该节点的连接会断一次）", sv.ID, sv.Name)
				c.notifyRestart(periodic, sv.ID, sv.Name)
			}
			if err != nil {
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
	c.invalidateRemoteCaches(serverID)

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
		panelCfg, err := c.st.BuildSingboxConfig(c.baseConfig, c.v2rayListen, byTag)
		if err != nil {
			return fmt.Errorf("local build config: %w", err)
		}
		if err := panelPathConflict(c.localConfigPath(), sv, cfg, panelCfg); err != nil {
			return err
		}
		if _, err := c.applyLocal(sv, cfg); err != nil {
			return fmt.Errorf("server %d apply: %w", sv.ID, err)
		}
		return nil
	}

	// Remote server — apply via SSH.
	if c.remoteMgr == nil {
		return fmt.Errorf("remote manager not configured")
	}
	serverCfg := SSHConfigFor(sv)
	applyCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	restarted, err := c.remoteMgr.ApplyConfig(applyCtx, serverCfg, cfg)
	if restarted {
		log.Printf("sbctl: server %d (%s) 配置有变化，已下发并重启 sing-box（该节点的连接会断一次）", sv.ID, sv.Name)
	}
	if err != nil {
		return fmt.Errorf("server %d apply: %w", sv.ID, err)
	}
	return nil
}

// SSHConfigFor projects a stored server row onto the SSH config the remote
// manager takes.
//
// Exported and single: there used to be four copies of this mapping (two here,
// one in api, two inlined in the remote-import handlers), and a field added to
// the server row reached some of them and silently missed the others. That is
// how you get a node whose "test connection" passes while every config deploy
// fails on it — the two paths were not dialling with the same settings.
func SSHConfigFor(sv *store.Server) *sshctl.ServerConfig {
	return &sshctl.ServerConfig{
		ID: sv.ID, Name: sv.Name, Host: sv.Host, Port: sv.Port,
		SSHUser: sv.SSHUser, SSHKey: sv.SSHKey, SSHKeyPass: sv.SSHKeyPass,
		SSHPassword: sv.SSHPassword, ConfigPath: sv.ConfigPath,
		SystemdUnit: sv.SystemdUnit, SingBoxBin: sv.SingBoxBin,
		V2rayListen: sv.V2rayListen, HostKey: sv.HostKey,
		UseSudo: sv.UseSudo, SudoPassword: sv.SudoPassword,
		SSHKeyPath: sv.SSHKeyPath,
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
			cfg := SSHConfigFor(sv)
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
	report(c.rebuildPeriodic()) // apply current state on startup
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
			report(c.rebuildPeriodic())
			c.refreshLocalVersion(false)
		}
	}
}
