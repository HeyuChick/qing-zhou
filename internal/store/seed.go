package store

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"time"

	"qingzhou/internal/config"

	"golang.org/x/crypto/bcrypt"
)

// SeedInfo reports what the seed step did.
type SeedInfo struct {
	AdminCreated   bool
	AdminUsername  string
	AdminPassword  string // only set when a random password was generated
	AdminGenerated bool
}

// Seed writes default settings, a JWT secret, and the initial admin account on
// first boot. It is idempotent: existing values are left untouched.
func (s *Store) Seed(cfg *config.Config) (SeedInfo, error) {
	info := SeedInfo{AdminUsername: cfg.AdminUsername}

	defaults := map[string]string{
		"registration_open":     "false",
		"email_verify_required": "true",
		"default_traffic":       "10737418240", // 10 GiB
		"default_expiry_days":   "30",
		"default_device_limit":  "3",
		"points_per_cny":        "10",
		"signup_bonus_points":   "0",
		// Monitor alert thresholds (percentages). CheckProbeAlerts reads these.
		"alert_cpu_threshold":   "90",
		"alert_mem_threshold":   "90",
		"alert_disk_threshold":  "85",
	}
	for k, v := range defaults {
		if err := s.setSettingIfAbsent(k, v); err != nil {
			return info, err
		}
	}

	// JWT secret: generate once and persist.
	if cur, err := s.GetSetting("jwt_secret"); err != nil {
		return info, err
	} else if cur == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return info, err
		}
		if err := s.SetSetting("jwt_secret", hex.EncodeToString(b)); err != nil {
			return info, err
		}
	}

	// Default admin: only if no admin exists yet.
	var admins int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&admins); err != nil {
		return info, err
	}
	if admins == 0 {
		password := cfg.AdminPassword
		if password == "" {
			// No QZ_ADMIN_PASS set: generate a random one and report it so the
			// operator can log in. Never ship a hardcoded default credential.
			b := make([]byte, 9)
			if _, err := rand.Read(b); err != nil {
				return info, err
			}
			password = base64.RawURLEncoding.EncodeToString(b)
			info.AdminPassword = password
			info.AdminGenerated = true
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return info, err
		}
		now := time.Now().Unix()
		_, err = s.db.Exec(
			`INSERT INTO users (username, password_hash, role, status, email_verified, created_at, updated_at)
			 VALUES (?, ?, 'admin', 'active', 1, ?, ?)`,
			cfg.AdminUsername, string(hash), now, now)
		if err != nil {
			return info, err
		}
		info.AdminCreated = true
	}

	return info, nil
}
