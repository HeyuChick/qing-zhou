package sshctl

import (
	"context"
	"qingzhou/internal/sbproc"
)

// InspectManagedVersion probes /proc/MainPID/exe with the exact privilege,
// systemd unit and config path used by the managed-process verification route.
func (m *RemoteManager) InspectManagedVersion(ctx context.Context, cfg *ServerConfig) (string, error) {
	unit := cfg.SystemdUnit
	if unit == "" {
		unit = "sing-box"
	}
	path := cfg.ConfigPath
	if path == "" {
		path = "/etc/sing-box/config.json"
	}
	script, err := sbproc.ManagedVersionScript(unit, path)
	if err != nil {
		return "", err
	}
	client, err := m.dial(ctx, cfg)
	if err != nil {
		return "", err
	}
	defer client.Close()
	out, err := m.runElevated(ctx, client, cfg, "sh -c "+shellQuote(script))
	if err != nil {
		return "", err
	}
	return sbproc.ParseManagedVersion(out)
}
