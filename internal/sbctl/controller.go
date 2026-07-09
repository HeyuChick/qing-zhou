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
	"context"
	"fmt"
	"log"
	"sync"
	"time"

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

	// Apply to each enabled remote server via SSH, concurrently — an
	// unreachable server must not stall the others (each dial can block up to
	// the SSH timeout) or the rebuilds behind this lock.
	if c.remoteMgr != nil {
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
	}

	return lastErr
}

// RebuildServer regenerates and applies the sing-box config for a single
// server. serverID=0 means the local panel server; any other value means a
// remote server managed via SSH.
func (c *Controller) RebuildServer(serverID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	byTag, err := c.st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		return err
	}

	if serverID == 0 {
		// Local server.
		cfg, err := c.st.BuildSingboxConfig(c.baseConfig, c.v2rayListen, byTag)
		if err != nil {
			return fmt.Errorf("local build config: %w", err)
		}
		if err := c.mgr.Apply(cfg); err != nil {
			return fmt.Errorf("local apply: %w", err)
		}
		return nil
	}

	// Remote server.
	if c.remoteMgr == nil {
		return fmt.Errorf("remote manager not configured")
	}
	sv, err := c.st.GetServer(serverID)
	if err != nil {
		return fmt.Errorf("get server %d: %w", serverID, err)
	}
	cfg, err := c.st.BuildSingboxConfigForServer(sv.ID, c.baseConfig, c.v2rayListen, byTag)
	if err != nil {
		return fmt.Errorf("server %d build config: %w", sv.ID, err)
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
