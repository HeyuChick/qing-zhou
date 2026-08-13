package config

import "os"

// Config holds runtime configuration. Everything has a sensible default so the
// binary runs out of the box; override via QZ_* environment variables.
type Config struct {
	ListenAddr    string
	DBPath        string
	AdminUsername string
	AdminPassword string
}

func Load() *Config {
	return &Config{
		// Reachable by default. A loopback default reads as the safer choice, but
		// this is a panel whose whole job is to be opened from somewhere else: it
		// made "装好了却打不开" the standard first experience, and the fix for it
		// was always to change this value anyway. Deployments behind nginx set
		// QZ_LISTEN explicitly — the one-click installer writes it either way, and
		// on upgrade it pins the old behaviour rather than letting this default
		// widen the reach of an install that is already running.
		ListenAddr:    env("QZ_LISTEN", "0.0.0.0:8081"),
		DBPath:        env("QZ_DB", "qingzhou.db"),
		AdminUsername: env("QZ_ADMIN_USER", "mllt992"),
		// Empty means: generate a random password on first-run seed and log it.
		AdminPassword: os.Getenv("QZ_ADMIN_PASS"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
