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
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"qingzhou/internal/sbproc"
	"qingzhou/internal/sbstats"
	"qingzhou/internal/sshctl"
	"qingzhou/internal/singbox"
	"qingzhou/internal/store"
)

// ConfigStore is the data layer the controller needs.
type ConfigStore interface {
	BuildUsersByTag(now int64) (map[string][]singbox.User, error)
	BuildSingboxConfig(base, v2rayListen string, usersByTag map[string][]singbox.User) ([]byte, error)
	BuildSingboxConfigForServer(serverID int64, base, v2rayListen string, usersByTag map[string][]singbox.User) ([]byte, error)
	AddUsageByClientName(name string, up, down int64) error
	ListServers() ([]*store.Server, error)
	GetServer(id int64) (*store.Server, error)
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
	st          ConfigStore
	mgr         Applier
	stats       StatsFetcher
	baseConfig  string
	v2rayListen string
	remoteMgr   *sshctl.RemoteManager // SSH manager for remote servers; nil if not configured

	mu sync.Mutex // serializes Rebuild
}

// New builds a Controller. baseConfig is the log/dns/route/outbounds template
// JSON; v2rayListen is the gRPC stats listen address embedded into the config.
func New(st ConfigStore, mgr Applier, stats StatsFetcher, baseConfig, v2rayListen string) *Controller {
	return &Controller{st: st, mgr: mgr, stats: stats, baseConfig: baseConfig, v2rayListen: v2rayListen}
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
		addrs, err := net.LookupHost(host)
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

	// No-op when config is byte-identical to what's already live.
	if cur, err := os.ReadFile(configPath); err == nil && bytes.Equal(cur, cfg) {
		return nil
	}

	// Write to a sibling temp file, validate, then atomic rename.
	dir := filepath.Dir(configPath)
	tmp, err := os.CreateTemp(dir, ".qz-sbcfg-*.tmp")
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

	// Atomic swap.
	if err := os.Rename(tmpPath, configPath); err != nil {
		return fmt.Errorf("install config: %w", err)
	}

	// Restart the systemd unit.
	rctx, rcancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer rcancel()
	rout, rerr := exec.CommandContext(rctx, "systemctl", "restart", unit).CombinedOutput()
	if rerr != nil {
		return fmt.Errorf("systemctl restart %s: %v: %s", unit, rerr, rout)
	}
	return nil
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
	if cfg, err := c.st.BuildSingboxConfig(c.baseConfig, c.v2rayListen, byTag); err != nil {
		lastErr = fmt.Errorf("local build config: %w", err)
		log.Printf("sbctl: local rebuild error: %v", err)
	} else if err := c.mgr.Apply(cfg); err != nil {
		lastErr = fmt.Errorf("local apply: %w", err)
		log.Printf("sbctl: local apply error: %v", err)
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
		cfg, err := c.st.BuildSingboxConfigForServer(sv.ID, c.baseConfig, c.v2rayListen, byTag)
		if err != nil {
			log.Printf("sbctl: server %d (%s) build config error: %v", sv.ID, sv.Name, err)
			setErr(fmt.Errorf("server %d build config: %w", sv.ID, err))
			continue
		}
		// Local server entry — apply directly without SSH.
		if isLocalHost(sv.Host) {
			if err := c.applyLocal(sv, cfg); err != nil {
				log.Printf("sbctl: server %d (%s) local apply error: %v", sv.ID, sv.Name, err)
				setErr(fmt.Errorf("server %d apply: %w", sv.ID, err))
			}
			continue
		}
		// Remote server — apply via SSH.
		if c.remoteMgr == nil {
			setErr(fmt.Errorf("server %d apply: remote manager not configured", sv.ID))
			continue
		}
		serverCfg := &sshctl.ServerConfig{
			ID:          sv.ID,
			Name:        sv.Name,
			Host:        sv.Host,
			Port:        sv.Port,
			SSHUser:     sv.SSHUser,
			SSHKey:      sv.SSHKey,
			SSHKeyPass:  sv.SSHKeyPass,
			SSHPassword: sv.SSHPassword,
			ConfigPath:  sv.ConfigPath,
			SystemdUnit: sv.SystemdUnit,
			SingBoxBin:  sv.SingBoxBin,
			V2rayListen: sv.V2rayListen,
			HostKey:     sv.HostKey,
		}
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
			}
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
	cfg, err := c.st.BuildSingboxConfigForServer(sv.ID, c.baseConfig, c.v2rayListen, byTag)
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
	serverCfg := &sshctl.ServerConfig{
		ID: sv.ID, Name: sv.Name, Host: sv.Host, Port: sv.Port,
		SSHUser: sv.SSHUser, SSHKey: sv.SSHKey, SSHKeyPass: sv.SSHKeyPass,
		SSHPassword: sv.SSHPassword, ConfigPath: sv.ConfigPath,
		SystemdUnit: sv.SystemdUnit, SingBoxBin: sv.SingBoxBin,
		V2rayListen: sv.V2rayListen, HostKey: sv.HostKey,
	}
	applyCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := c.remoteMgr.ApplyConfig(applyCtx, serverCfg, cfg); err != nil {
		return fmt.Errorf("server %d apply: %w", sv.ID, err)
	}
	return nil
}

// CollectStats polls per-user traffic deltas and accumulates them onto users.
// Returns the number of users whose usage changed.
func (c *Controller) CollectStats(ctx context.Context) (int, error) {
	m, err := c.stats.QueryUserTraffic(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for name, t := range m {
		if t.Up == 0 && t.Down == 0 {
			continue
		}
		if err := c.st.AddUsageByClientName(name, t.Up, t.Down); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
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
	report(c.Rebuild()) // apply current state on startup
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
		}
	}
}
