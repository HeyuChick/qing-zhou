package store

import "time"

type DeviceAddon struct {
	ID        int64 `json:"id"`
	Slots     int64 `json:"slots"`
	ExpiresAt int64 `json:"expires_at"`
}

// ListActiveDeviceAddons returns a user's currently-active device add-ons.
func (s *Store) ListActiveDeviceAddons(userID int64) ([]DeviceAddon, error) {
	rows, err := s.db.Query(
		`SELECT id, slots, expires_at FROM device_addons WHERE user_id=? AND expires_at>? ORDER BY expires_at`,
		userID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceAddon{}
	for rows.Next() {
		var a DeviceAddon
		if err := rows.Scan(&a.ID, &a.Slots, &a.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
