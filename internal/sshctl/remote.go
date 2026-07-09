// Package sshctl provides SSH-based remote management for sing-box
// configuration deployment across multiple VPS nodes.
package sshctl

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// parsePrivateKey parses a PEM-encoded private key, using the passphrase only
// when one is set. Passing a passphrase to an unencrypted OpenSSH key fails
// with "key is not password protected", so empty-passphrase keys must use the
// plain parser.
func parsePrivateKey(pem, passphrase string) (ssh.Signer, error) {
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase([]byte(pem), []byte(passphrase))
	}
	return ssh.ParsePrivateKey([]byte(pem))
}

// ServerConfig holds the SSH connection details and sing-box paths
// for a single remote server.
type ServerConfig struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	SSHUser     string `json:"ssh_user"`
	SSHKey      string `json:"ssh_key"`       // path to private key file
	SSHKeyPass  string `json:"ssh_key_pass"`  // passphrase for encrypted key
	SSHPassword string `json:"ssh_password"`  // password auth fallback
	ConfigPath  string `json:"config_path"`   // e.g. /etc/sing-box/config.json
	SystemdUnit string `json:"systemd_unit"`  // e.g. sing-box
	SingBoxBin  string `json:"sing_box_bin"`  // e.g. /usr/local/bin/sing-box
	V2rayListen string `json:"v2ray_listen"`  // e.g. 127.0.0.1
	HostKey     string `json:"-"`             // pinned SSH host key (authorized_keys line); "" = pin on first use
}

// RemoteManager coordinates SSH operations against multiple remote servers.
type RemoteManager struct {
	timeout time.Duration

	// persistHostKey pins a server's SSH host key on first successful connect
	// (TOFU). nil disables persistence (verification against an already-pinned
	// key still applies).
	persistHostKey func(serverID int64, hostKey string) error

	// lastConfigHash tracks the SHA-256 of the last successfully applied
	// config per server (keyed by host:configPath). When Rebuild() is
	// called every minute but the config hasn't changed, this avoids
	// needlessly writing the file and restarting sing-box — which would
	// drop every active connection.
	mu           sync.Mutex
	lastConfigHash map[string]string
}

// Option configures a RemoteManager.
type Option func(*RemoteManager)

// WithTimeout sets the dial/command timeout (default 30s).
func WithTimeout(d time.Duration) Option {
	return func(m *RemoteManager) { m.timeout = d }
}

// New creates a RemoteManager with the given options.
func New(opts ...Option) *RemoteManager {
	m := &RemoteManager{
		timeout: 30 * time.Second,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// SetHostKeyPersister registers a callback used to pin a server's SSH host key
// on first successful connection (trust-on-first-use).
func (m *RemoteManager) SetHostKeyPersister(fn func(serverID int64, hostKey string) error) {
	m.persistHostKey = fn
}

// ──────────────────────────────────────────────────────────────
// SSH client construction
// ──────────────────────────────────────────────────────────────

func (m *RemoteManager) buildClientConfig(cfg *ServerConfig) (*ssh.ClientConfig, error) {
	authMethods, err := m.buildAuthMethods(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh auth: %w", err)
	}
	if len(authMethods) == 0 {
		return nil, errors.New("ssh: no authentication method configured (set password or key)")
	}

	return &ssh.ClientConfig{
		User:            cfg.SSHUser,
		Auth:            authMethods,
		HostKeyCallback: m.hostKeyCallback(cfg),
		Timeout:         m.timeout,
	}, nil
}

// hostKeyCallback verifies the remote host key. If the server already has a
// pinned key, the presented key must match it (mismatch = possible MITM →
// refuse). Otherwise the key is trusted on first use and pinned via the
// persister, so every later connection is verified. This replaces the previous
// InsecureIgnoreHostKey, which let any MITM impersonate a landing server and
// harvest SSH credentials / push an attacker-chosen config (root RCE).
func (m *RemoteManager) hostKeyCallback(cfg *ServerConfig) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		presented := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if cfg.HostKey != "" {
			if subtle.ConstantTimeCompare([]byte(cfg.HostKey), []byte(presented)) == 1 {
				return nil
			}
			return fmt.Errorf("ssh host key mismatch for %s: refusing (possible MITM; clear the pinned key to re-trust)", cfg.Host)
		}
		// Trust on first use: pin the key so subsequent connections are verified.
		if m.persistHostKey != nil && cfg.ID != 0 {
			if err := m.persistHostKey(cfg.ID, presented); err != nil {
				return fmt.Errorf("pin host key: %w", err)
			}
		}
		cfg.HostKey = presented
		return nil
	}
}

func (m *RemoteManager) buildAuthMethods(cfg *ServerConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// 1) SSH private key. SSHKey holds the PEM key *content* (pasted in the
	// admin UI and stored encrypted), not a file path.
	if cfg.SSHKey != "" {
		signer, err := parsePrivateKey(cfg.SSHKey, cfg.SSHKeyPass)
		if err != nil {
			return nil, fmt.Errorf("parse key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// 2) Password
	if cfg.SSHPassword != "" {
		methods = append(methods, ssh.Password(cfg.SSHPassword))
	}

	return methods, nil
}

func (m *RemoteManager) dial(ctx context.Context, cfg *ServerConfig) (*ssh.Client, error) {
	cc, err := m.buildClientConfig(cfg)
	if err != nil {
		return nil, err
	}
	addr := netAddr(cfg.Host, cfg.Port)

	// The x/crypto/ssh client does not accept context directly, so we
	// wrap the dial with a goroutine+select for context cancellation.
	type result struct {
		client *ssh.Client
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		c, e := ssh.Dial("tcp", addr, cc)
		ch <- result{c, e}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.client, r.err
	}
}

// ──────────────────────────────────────────────────────────────
// Public API
// ──────────────────────────────────────────────────────────────

// ApplyConfig writes configJSON to the remote server's ConfigPath,
// validates it, and restarts the sing-box service.
// Skips the write+restart when the config is byte-identical to the last
// successfully applied config for this server, preventing needless restarts
// that would drop every active connection.
func (m *RemoteManager) ApplyConfig(ctx context.Context, cfg *ServerConfig, configJSON []byte) error {
	// Fast path: skip if config hasn't changed since last successful apply.
	hash := configHash(configJSON)
	cacheKey := cfg.Host + ":" + cfg.ConfigPath
	m.mu.Lock()
	if m.lastConfigHash != nil && m.lastConfigHash[cacheKey] == hash {
		m.mu.Unlock()
		return nil // no-op: config identical to what's already live
	}
	m.mu.Unlock()

	client, err := m.dial(ctx, cfg)
	if err != nil {
		return fmt.Errorf("ssh connect %s: %w", cfg.Host, err)
	}
	defer client.Close()

	// Write to a temp file first, validate it, and only then move it into
	// place. This keeps the live config intact if the new one is broken or
	// the transfer is interrupted, so a bad config never takes the node down.
	tmpPath := cfg.ConfigPath + ".qz-new"
	if err := m.writeFile(ctx, client, tmpPath, configJSON); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := m.validateConfigPath(ctx, client, cfg, tmpPath); err != nil {
		_, _ = m.run(ctx, client, "rm -f "+shellQuote(tmpPath))
		return fmt.Errorf("validate config: %w", err)
	}
	if _, err := m.run(ctx, client, fmt.Sprintf("mv -f %s %s", shellQuote(tmpPath), shellQuote(cfg.ConfigPath))); err != nil {
		return fmt.Errorf("install config: %w", err)
	}

	if err := m.restartService(ctx, client, cfg.SystemdUnit); err != nil {
		return fmt.Errorf("restart service: %w", err)
	}

	// Record successful apply so the next tick with identical config is a no-op.
	m.mu.Lock()
	if m.lastConfigHash == nil {
		m.lastConfigHash = make(map[string]string)
	}
	m.lastConfigHash[cacheKey] = hash
	m.mu.Unlock()

	return nil
}

// TestConnection verifies SSH connectivity and returns the remote
// sing-box version string on success.
func (m *RemoteManager) TestConnection(ctx context.Context, cfg *ServerConfig) (string, error) {
	client, err := m.dial(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("ssh connect %s: %w", cfg.Host, err)
	}
	defer client.Close()

	bin := cfg.SingBoxBin
	if bin == "" {
		bin = "sing-box"
	}
	// Fall back to `sing-box` from PATH if the configured binary isn't executable.
	cmd := fmt.Sprintf(`BIN=%s; [ -x "$BIN" ] || BIN=sing-box; "$BIN" version 2>/dev/null || echo unknown`, shellQuote(bin))
	out, err := m.run(ctx, client, cmd)
	if err != nil {
		return "", fmt.Errorf("run version: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// RestartService restarts the systemd unit on the remote server.
func (m *RemoteManager) RestartService(ctx context.Context, cfg *ServerConfig) error {
	client, err := m.dial(ctx, cfg)
	if err != nil {
		return fmt.Errorf("ssh connect %s: %w", cfg.Host, err)
	}
	defer client.Close()
	return m.restartService(ctx, client, cfg.SystemdUnit)
}

// ConfigFileInfo describes a .json file found on the remote server.
type ConfigFileInfo struct {
	Path string `json:"path"`
	Size int64  `json:"size"` // bytes
}

// ListRemoteConfigFiles scans common sing-box config directories on the
// remote server and returns all .json files found, sorted by modification
// time (newest first).  Also includes the systemd ExecStart -c path and
// the stored ConfigPath if they point to unique locations.
func (m *RemoteManager) ListRemoteConfigFiles(ctx context.Context, cfg *ServerConfig) ([]ConfigFileInfo, error) {
	client, err := m.dial(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s: %w", cfg.Host, err)
	}
	defer client.Close()

	unit := cfg.SystemdUnit
	if unit == "" {
		unit = "sing-box"
	}

	// 1) Get systemd ExecStart -c path
	detectCmd := fmt.Sprintf(
		`systemctl cat %s 2>/dev/null | grep -oP '(?<=-c\s)\S+' | head -1`,
		shellQuote(unit),
	)
	detectedPath, _ := m.run(ctx, client, detectCmd)
	detectedPath = strings.TrimSpace(detectedPath)

	// 2) Scan common directories for .json files
	// find returns: "size\tmtime\tfilepath" lines, sorted by mtime desc
	scanCmd := `find /etc/s-box /etc/sing-box /usr/local/etc/sing-box /etc/x-ui ` +
		`-maxdepth 2 -name '*.json' -type f ` +
		`-printf '%s\t%T@\t%p\n' 2>/dev/null | sort -t$'\t' -k2 -rn | head -20`
	scanOut, _ := m.run(ctx, client, scanCmd)

	seen := map[string]bool{}
	var files []ConfigFileInfo

	// Helper to add if unique
	addPath := func(p string, sizeHint int64) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		files = append(files, ConfigFileInfo{Path: p, Size: sizeHint})
	}

	// Parse find output
	for _, line := range strings.Split(scanOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		var sz int64
		fmt.Sscanf(parts[0], "%d", &sz)
		addPath(parts[2], sz)
	}

	// Ensure detected and stored paths are in the list
	if detectedPath != "" {
		addPath(detectedPath, 0)
	}
	if cfg.ConfigPath != "" {
		addPath(cfg.ConfigPath, 0)
	}

	// Always include standard sing-box config paths
	addPath("/etc/sing-box/config.json", 0)
	addPath("/etc/s-box/sb.json", 0)

	return files, nil
}

// ReadRemoteConfigAtPath reads a specific config file from a remote server
// by path. No auto-detection — the caller provides the exact path.
func (m *RemoteManager) ReadRemoteConfigAtPath(ctx context.Context, cfg *ServerConfig, configPath string) ([]byte, error) {
	client, err := m.dial(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s: %w", cfg.Host, err)
	}
	defer client.Close()

	raw, err := m.run(ctx, client, "cat "+shellQuote(configPath)+" 2>/dev/null")
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", configPath, err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("文件 %s 为空", configPath)
	}
	return []byte(raw), nil
}

// ReadRemoteConfig reads the sing-box config JSON from a remote server.
// It auto-detects the actual config path by trying multiple candidates
// and returning the first one that contains a non-empty inbounds array.
func (m *RemoteManager) ReadRemoteConfig(ctx context.Context, cfg *ServerConfig) ([]byte, string, error) {
	client, err := m.dial(ctx, cfg)
	if err != nil {
		return nil, "", fmt.Errorf("ssh connect %s: %w", cfg.Host, err)
	}
	defer client.Close()

	bin := cfg.SingBoxBin
	if bin == "" {
		bin = "sing-box"
	}
	unit := cfg.SystemdUnit
	if unit == "" {
		unit = "sing-box"
	}

	// Strategy: read the systemd unit file to find the real -c path.
	// This handles cases where config_path in the DB is wrong (e.g. /etc/sing-box/config.json
	// but the actual service uses /etc/s-box/sb.json).
	detectCmd := fmt.Sprintf(
		`unit=$(systemctl cat %s 2>/dev/null | grep -oP '(?<=-c\s)\S+' | head -1); `+
			`if [ -n "$unit" ]; then echo "$unit"; else echo ""; fi`,
		shellQuote(unit),
	)
	detectedPath, _ := m.run(ctx, client, detectCmd)
	detectedPath = strings.TrimSpace(detectedPath)

	// Build candidate paths: detected > stored > common defaults
	var candidates []string
	if detectedPath != "" {
		candidates = append(candidates, detectedPath)
	}
	if cfg.ConfigPath != "" {
		candidates = append(candidates, cfg.ConfigPath)
	}
	// Common defaults (avoid duplicates)
	for _, p := range []string{"/etc/s-box/sb.json", "/etc/sing-box/config.json", "/usr/local/etc/sing-box/config.json"} {
		if p != detectedPath && p != cfg.ConfigPath {
			candidates = append(candidates, p)
		}
	}

	for _, path := range candidates {
		raw, err := m.run(ctx, client, "cat "+shellQuote(path)+" 2>/dev/null")
		if err != nil || strings.TrimSpace(raw) == "" {
			continue
		}
		// Must contain "inbounds" with a non-empty array — skip files
		// that only have "inbounds": [] or no inbounds at all.
		if !strings.Contains(raw, "inbounds") {
			continue
		}
		// Quick check: there must be at least one opening brace after "inbounds"
		// to confirm a non-empty array.  "inbounds": [  {  counts.
		idx := strings.Index(raw, "inbounds")
		after := raw[idx:]
		if !strings.Contains(after[:min(len(after), 500)], "{") {
			continue
		}
		return []byte(strings.TrimSpace(raw)), path, nil
	}

	return nil, "", fmt.Errorf("未找到有效的 sing-box 配置文件（已尝试路径: %v）", candidates)
}

// ──────────────────────────────────────────────────────────────
// Low-level helpers
// ──────────────────────────────────────────────────────────────

// writeFile uploads data to the remote path via SFTP-like behaviour
// using a shell pipe (avoids needing a sftp sub-dependency).
func (m *RemoteManager) writeFile(ctx context.Context, client *ssh.Client, remotePath string, data []byte) error {
	// Ensure remote directory exists.
	dir := filepath.Dir(remotePath)
	if _, err := m.run(ctx, client, "mkdir -p "+shellQuote(dir)); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// Use a cat-heredoc approach for reliable transfer.
	// The delimiter is unlikely to appear in a JSON config.
	// umask 077 + explicit chmod so the file (which embeds the Reality private
	// key, user passwords and UUIDs) is never world-readable to other local
	// users on the landing host.
	const delim = "__SSHCTL_EOF_8f3a__"
	script := fmt.Sprintf(
		`umask 077; cat > %s << '%s'
%s
%s
chmod 600 %s
`,
		shellQuote(remotePath), delim, string(data), delim, shellQuote(remotePath),
	)

	if _, err := m.run(ctx, client, script); err != nil {
		return fmt.Errorf("cat write: %w", err)
	}
	return nil
}

// validateConfigPath runs "sing-box check" against the given config path on
// the remote server.
func (m *RemoteManager) validateConfigPath(ctx context.Context, client *ssh.Client, cfg *ServerConfig, path string) error {
	bin := cfg.SingBoxBin
	if bin == "" {
		bin = "sing-box"
	}
	// Shell logic: if the configured binary isn't executable, fall back to
	// `sing-box` from PATH. This handles stale sing_box_bin values gracefully.
	cmd := fmt.Sprintf(`BIN=%s; [ -x "$BIN" ] || BIN=sing-box; "$BIN" check -c %s 2>&1`,
		shellQuote(bin), shellQuote(path))
	out, err := m.run(ctx, client, cmd)
	if err != nil {
		return fmt.Errorf("sing-box check failed: %s: %w", out, err)
	}
	return nil
}

// restartService restarts (or starts) a systemd unit.
func (m *RemoteManager) restartService(ctx context.Context, client *ssh.Client, unit string) error {
	if unit == "" {
		unit = "sing-box"
	}
	// Try restart first; if unit is inactive, start it.
	q := shellQuote(unit)
	cmd := fmt.Sprintf("systemctl restart %s 2>&1 || systemctl start %s 2>&1", q, q)
	out, err := m.run(ctx, client, cmd)
	if err != nil {
		return fmt.Errorf("systemctl: %s: %w", out, err)
	}
	return nil
}

// run executes a shell command on the remote host and returns combined output.
func (m *RemoteManager) run(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	var stdout, stderr strings.Builder
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Start(cmd); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- session.Wait() }()

	select {
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	case err := <-done:
		combined := strings.TrimSpace(stdout.String() + " " + stderr.String())
		if err != nil {
			return combined, fmt.Errorf("exit: %w: %s", err, combined)
		}
		return combined, nil
	}
}

// ──────────────────────────────────────────────────────────────
// Utilities
// ──────────────────────────────────────────────────────────────

func netAddr(host string, port int) string {
	if port <= 0 {
		port = 22
	}
	// net.JoinHostPort brackets IPv6 literals correctly.
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// shellQuote wraps a path in single quotes for safe shell usage.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// configHash returns a hex-encoded SHA-256 of the config bytes.
func configHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

