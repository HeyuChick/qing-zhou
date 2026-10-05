package sshctl

import (
	"context"
	"qingzhou/internal/sbproc"
)

// InspectManagedProcess uses the same privilege path as ApplyConfig, because
// its installed 0600 file may only be readable by root.
func (m *RemoteManager) InspectManagedProcess(ctx context.Context, cfg *ServerConfig) (sbproc.ManagedProcess, error) {
	unit := cfg.SystemdUnit
	if unit == "" {
		unit = "sing-box"
	}
	script, err := sbproc.ManagedProcessScript(unit, cfg.ConfigPath)
	if err != nil {
		return sbproc.ManagedProcess{}, err
	}
	client, err := m.dial(ctx, cfg)
	if err != nil {
		return sbproc.ManagedProcess{}, err
	}
	defer client.Close()
	out, err := m.runElevated(ctx, client, cfg, "sh -c "+shellQuote(script))
	if err != nil {
		return sbproc.ManagedProcess{}, err
	}
	return sbproc.ParseManagedProcess(out)
}

// InstalledConfigHash follows the exact bytes written by the SSH heredoc.
func InstalledConfigHash(raw []byte) string { return configHash(remoteBytes(raw)) }
