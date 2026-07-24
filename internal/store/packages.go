package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Package struct {
	ID           int64    `json:"id"`
	Type         string   `json:"type"` // traffic | plan | device
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Highlights   []string `json:"highlights"` // selling-point bullets shown in the shop
	PricePoints  int64    `json:"price_points"`
	TrafficBytes int64    `json:"traffic_bytes"`
	DeviceAdd    int64    `json:"device_add"`
	DurationDays int64    `json:"duration_days"`
	Stock        int64    `json:"stock"` // -1 = unlimited
	Enabled      bool     `json:"enabled"`
	SortOrder    int64    `json:"sort_order"`
	CreatedAt    int64    `json:"created_at"`
	GroupIDs     []int64  `json:"group_ids,omitempty"`      // plan↔node-groups: which nodes it grants (not a column)
	UserGroupIDs []int64  `json:"user_group_ids,omitempty"` // package↔user-groups: who may buy it; empty = public (not a column)
	Subscribers  int64    `json:"subscribers,omitempty"`    // users currently on this plan (not a column)
}

const pkgCols = `id, type, name, description, highlights, price_points, traffic_bytes, device_add,
	duration_days, stock, enabled, sort_order, created_at`

func scanPackage(sc scanner) (*Package, error) {
	var p Package
	var highlights string
	err := sc.Scan(&p.ID, &p.Type, &p.Name, &p.Description, &highlights, &p.PricePoints, &p.TrafficBytes,
		&p.DeviceAdd, &p.DurationDays, &p.Stock, &p.Enabled, &p.SortOrder, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Highlights = decodeHighlights(highlights)
	return &p, nil
}

// decodeHighlights parses the stored JSON array of selling points. A blank value
// (legacy rows, or a package with none) yields nil rather than an error.
func decodeHighlights(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// encodeHighlights serialises the selling-point list for storage, dropping blank
// entries so the shop never renders an empty bullet. Empty list → "" (not "[]"),
// so a package with no highlights reads back as nil.
func encodeHighlights(h []string) string {
	clean := make([]string, 0, len(h))
	for _, x := range h {
		if t := strings.TrimSpace(x); t != "" {
			clean = append(clean, t)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	b, _ := json.Marshal(clean)
	return string(b)
}

func (s *Store) GetPackage(id int64) (*Package, error) {
	return scanPackage(s.db.QueryRow(`SELECT `+pkgCols+` FROM packages WHERE id=?`, id))
}

// ListPackages returns every package ordered for display (admin view). The
// user-facing shop uses ListPackagesForUser, which also applies the on-sale and
// user-group filters.
func (s *Store) ListPackages() ([]*Package, error) {
	rows, err := s.db.Query(`SELECT ` + pkgCols + ` FROM packages ORDER BY sort_order ASC, id ASC`)
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

// PackageNames returns id→current name for every package, so a user's plan
// buckets can display the package's live name instead of the snapshot taken at
// purchase (renaming a package should propagate to everyone who holds it). Only
// real packages appear; buckets for the pool/free/welcome/admin grants have no
// package row and keep their own name.
func (s *Store) PackageNames() (map[int64]string, error) {
	rows, err := s.db.Query(`SELECT id, name FROM packages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// ListPackagesForUser returns the on-sale packages userID is allowed to buy:
// the public ones (no user-group bindings) plus those bound to a group the user
// belongs to. Restricted packages the user has no claim on are hidden outright.
//
// This is only the display half of the rule — Purchase re-checks inside its
// transaction, so hiding a package here is not what makes it unbuyable.
func (s *Store) ListPackagesForUser(userID int64) ([]*Package, error) {
	rows, err := s.db.Query(`SELECT `+pkgCols+` FROM packages p
		WHERE p.enabled=1 AND p.type!='device' AND (p.stock<0 OR p.stock>0)
		  AND (NOT EXISTS (SELECT 1 FROM package_user_groups g WHERE g.package_id=p.id)
		       OR EXISTS (SELECT 1 FROM package_user_groups g
		                  JOIN user_group_members m ON m.group_id=g.group_id
		                  WHERE g.package_id=p.id AND m.user_id=?))
		ORDER BY p.sort_order ASC, p.id ASC`, userID)
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
		(type, name, description, highlights, price_points, traffic_bytes, device_add, duration_days, stock, enabled, sort_order, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Type, p.Name, p.Description, encodeHighlights(p.Highlights), p.PricePoints, p.TrafficBytes, p.DeviceAdd,
		p.DurationDays, p.Stock, boolToInt(p.Enabled), p.SortOrder, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdatePackage(p Package) error {
	_, err := s.db.Exec(`UPDATE packages SET
		type=?, name=?, description=?, highlights=?, price_points=?, traffic_bytes=?, device_add=?,
		duration_days=?, stock=?, enabled=?, sort_order=? WHERE id=?`,
		p.Type, p.Name, p.Description, encodeHighlights(p.Highlights), p.PricePoints, p.TrafficBytes, p.DeviceAdd,
		p.DurationDays, p.Stock, boolToInt(p.Enabled), p.SortOrder, p.ID)
	return err
}

// ReorderPackages sets sort_order to each id's position in the given slice, so
// the shop and admin list (both ORDER BY sort_order) render in this exact order.
// Ids not present keep their old sort_order and thus sort after the reordered
// ones (or interleave by their stale value) — callers pass the full list.
func (s *Store) ReorderPackages(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE packages SET sort_order=? WHERE id=?`, i, id); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	if _, err := tx.Exec(`DELETE FROM package_user_groups WHERE package_id=?`, id); err != nil {
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

// PackagePlanHolders returns the ids of users who hold a live plan bucket for
// this package. Unlike a lookup on the legacy users.current_plan_id pointer (which
// records only the SINGLE most recently purchased plan), this reflects the bucket
// model: a user may hold several plans at once, so retire/delete must act on the
// real buckets or they silently skip stacked/older subscribers.
func (s *Store) PackagePlanHolders(pkgID int64) ([]int64, error) {
	return queryInts(s.db, `SELECT DISTINCT user_id FROM user_plans
		WHERE kind='plan' AND package_id=? ORDER BY user_id`, pkgID)
}

// PlanSubscriberCounts maps package id → number of users holding that plan,
// counted from the authoritative buckets (not current_plan_id).
func (s *Store) PlanSubscriberCounts() (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT package_id, COUNT(DISTINCT user_id) FROM user_plans
		WHERE kind='plan' AND package_id>0 GROUP BY package_id`)
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

// RefundableOrdersForPackage returns every non-refunded successful order id for
// (user, package), oldest first, so retire can refund each stacked purchase — not
// merely the latest one.
func (s *Store) RefundableOrdersForPackage(userID, pkgID int64) ([]int64, error) {
	return queryInts(s.db, `SELECT id FROM orders WHERE user_id=? AND package_id=? AND status='success'
		ORDER BY created_at, id`, userID, pkgID)
}

// ClearPlanBucket removes any plan bucket the user holds for this package, nulls
// the legacy current_plan_id pointer if it still points here, and recomputes the
// user aggregate — all in one transaction. Used by retire to fully revoke a
// package after its orders are refunded (refunds shrink the bucket but may leave a
// used-up remnant), and idempotent so re-running is safe.
func (s *Store) ClearPlanBucket(userID, pkgID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`DELETE FROM user_plans WHERE user_id=? AND kind='plan' AND package_id=?`, userID, pkgID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE users SET current_plan_id=NULL WHERE id=? AND current_plan_id=?`, userID, pkgID); err != nil {
		return err
	}
	if _, _, _, _, err := recomputeUserAggregate(tx, userID, time.Now().Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
