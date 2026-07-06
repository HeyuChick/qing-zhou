package store

import (
	"database/sql"
	"errors"
	"time"
)

type Package struct {
	ID           int64  `json:"id"`
	Type         string `json:"type"` // traffic | plan | device
	Name         string `json:"name"`
	Description  string `json:"description"`
	PricePoints  int64  `json:"price_points"`
	TrafficBytes int64  `json:"traffic_bytes"`
	DeviceAdd    int64  `json:"device_add"`
	DurationDays int64  `json:"duration_days"`
	Stock        int64  `json:"stock"` // -1 = unlimited
	Enabled      bool    `json:"enabled"`
	SortOrder    int64   `json:"sort_order"`
	CreatedAt    int64   `json:"created_at"`
	GroupIDs     []int64 `json:"group_ids,omitempty"`  // plan↔node-groups (not a column)
	Subscribers  int64   `json:"subscribers,omitempty"` // users currently on this plan (not a column)
}

const pkgCols = `id, type, name, description, price_points, traffic_bytes, device_add,
	duration_days, stock, enabled, sort_order, created_at`

func scanPackage(sc scanner) (*Package, error) {
	var p Package
	err := sc.Scan(&p.ID, &p.Type, &p.Name, &p.Description, &p.PricePoints, &p.TrafficBytes,
		&p.DeviceAdd, &p.DurationDays, &p.Stock, &p.Enabled, &p.SortOrder, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) GetPackage(id int64) (*Package, error) {
	return scanPackage(s.db.QueryRow(`SELECT `+pkgCols+` FROM packages WHERE id=?`, id))
}

// ListPackages returns packages ordered for display. If enabledOnly, hides
// disabled and out-of-stock items (for the user-facing shop).
func (s *Store) ListPackages(enabledOnly bool) ([]*Package, error) {
	q := `SELECT ` + pkgCols + ` FROM packages`
	if enabledOnly {
		q += ` WHERE enabled=1 AND type!='device' AND (stock<0 OR stock>0)`
	}
	q += ` ORDER BY sort_order ASC, id ASC`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Package
	for rows.Next() {
		p, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreatePackage(p Package) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO packages
		(type, name, description, price_points, traffic_bytes, device_add, duration_days, stock, enabled, sort_order, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		p.Type, p.Name, p.Description, p.PricePoints, p.TrafficBytes, p.DeviceAdd,
		p.DurationDays, p.Stock, boolToInt(p.Enabled), p.SortOrder, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdatePackage(p Package) error {
	_, err := s.db.Exec(`UPDATE packages SET
		type=?, name=?, description=?, price_points=?, traffic_bytes=?, device_add=?,
		duration_days=?, stock=?, enabled=?, sort_order=? WHERE id=?`,
		p.Type, p.Name, p.Description, p.PricePoints, p.TrafficBytes, p.DeviceAdd,
		p.DurationDays, p.Stock, boolToInt(p.Enabled), p.SortOrder, p.ID)
	return err
}

func (s *Store) DeletePackage(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Drop the package's group bindings so plan_groups doesn't keep rows for a
	// package that no longer exists. (Callers guard against deleting a package
	// that still has subscribers, so current_plan_id is not orphaned here.)
	if _, err := tx.Exec(`DELETE FROM plan_groups WHERE package_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM packages WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetPackageEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`UPDATE packages SET enabled=? WHERE id=?`, boolToInt(enabled), id)
	return err
}

// PlanSubscribers returns the ids of users whose current plan is this package.
func (s *Store) PlanSubscribers(pkgID int64) ([]int64, error) {
	return queryInts(s.db, `SELECT id FROM users WHERE current_plan_id=?`, pkgID)
}

// PlanSubscriberCounts maps package id → number of users currently on that plan.
func (s *Store) PlanSubscriberCounts() (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT current_plan_id, COUNT(*) FROM users WHERE current_plan_id IS NOT NULL GROUP BY current_plan_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var pid, n int64
		if err := rows.Scan(&pid, &n); err != nil {
			return nil, err
		}
		out[pid] = n
	}
	return out, rows.Err()
}

// LatestRefundableOrderForPackage returns the most recent non-refunded successful
// order id for (user, package), or 0 if none.
func (s *Store) LatestRefundableOrderForPackage(userID, pkgID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM orders WHERE user_id=? AND package_id=? AND status='success'
		ORDER BY created_at DESC, id DESC LIMIT 1`, userID, pkgID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
