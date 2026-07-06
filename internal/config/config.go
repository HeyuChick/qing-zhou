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
		ListenAddr:    env("QZ_LISTEN", "127.0.0.1:8081"),
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
