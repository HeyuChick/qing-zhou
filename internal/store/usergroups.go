package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// User groups answer "who may buy this package". They are a separate axis from
// node_groups (see nodes.go), which answer "which nodes does a bought package
// grant". A package with no rows in package_user_groups is public.

type UserGroup struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SortOrder   int64  `json:"sort_order"`
	CreatedAt   int64  `json:"created_at"`
	Members     int64  `json:"members,omitempty"` // users in this group (not a column)
}

func (s *Store) ListUserGroups() ([]*UserGroup, error) {
	rows, err := s.db.Query(`SELECT id, name, description, sort_order, created_at
		FROM user_groups ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UserGroup
	for rows.Next() {
		var g UserGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.SortOrder, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

func (s *Store) GetUserGroup(id int64) (*UserGroup, error) {
	var g UserGroup
	err := s.db.QueryRow(`SELECT id, name, description, sort_order, created_at
		FROM user_groups WHERE id=?`, id).
		Scan(&g.ID, &g.Name, &g.Description, &g.SortOrder, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Store) CreateUserGroup(g UserGroup) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO user_groups (name, description, sort_order, created_at)
		VALUES (?,?,?,?)`, g.Name, g.Description, g.SortOrder, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateUserGroup(g UserGroup) error {
	_, err := s.db.Exec(`UPDATE user_groups SET name=?, description=?, sort_order=? WHERE id=?`,
		g.Name, g.Description, g.SortOrder, g.ID)
	return err
}

// DeleteUserGroup drops the group and every reference to it. Note the effect on
// packages restricted to only this group: they lose their last binding and so
// become public rather than unbuyable. Callers warn the admin about that.
func (s *Store) DeleteUserGroup(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM user_group_members WHERE group_id=?`,
		`DELETE FROM package_user_groups WHERE group_id=?`,
		`DELETE FROM reg_code_user_groups WHERE group_id=?`,
		`DELETE FROM user_groups WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UserGroupMemberCounts maps group id → number of users in it.
func (s *Store) UserGroupMemberCounts() (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT group_id, COUNT(*) FROM user_group_members GROUP BY group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var gid, n int64
		if err := rows.Scan(&gid, &n); err != nil {
			return nil, err
		}
		out[gid] = n
	}
	return out, rows.Err()
}

// PackagesRestrictedToOnly returns the ids of packages whose ONLY binding is
// this group — i.e. the packages that would silently turn public if it were
// deleted.
func (s *Store) PackagesRestrictedToOnly(groupID int64) ([]int64, error) {
	return queryInts(s.db, `SELECT package_id FROM package_user_groups
		GROUP BY package_id
		HAVING COUNT(*)=1 AND MAX(group_id)=?`, groupID)
}

// ---- user ↔ groups ----

func (s *Store) SetUserGroups(userID int64, groupIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM user_group_members WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, gid := range groupIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_group_members (user_id, group_id)
			SELECT ?, id FROM user_groups WHERE id=?`, userID, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetGroupMembers replaces a group's entire membership in one transaction.
// This is the group-side counterpart of SetUserGroups: the admin UI edits
// "who is in this group", and doing that as N per-user updates would be
// non-atomic (a failure halfway leaves the group half-applied). Unknown user
// ids are ignored, mirroring how the other setters ignore unknown group ids.
func (s *Store) SetGroupMembers(groupID int64, userIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM user_group_members WHERE group_id=?`, groupID); err != nil {
		return err
	}
	for _, uid := range userIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_group_members (user_id, group_id)
			SELECT id, ? FROM users WHERE id=?`, groupID, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddUserGroups joins a user into groups without dropping existing membership
// (used by registration codes, which grant rather than replace).
func (s *Store) AddUserGroups(userID int64, groupIDs []int64) error {
	if len(groupIDs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, gid := range groupIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_group_members (user_id, group_id)
			SELECT ?, id FROM user_groups WHERE id=?`, userID, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UserGroupIDs(userID int64) ([]int64, error) {
	return queryInts(s.db, `SELECT group_id FROM user_group_members WHERE user_id=? ORDER BY group_id`, userID)
}

// userColsPrefixed qualifies userCols with a table alias for joins. It splits on
// "," (not ", ") and trims, because userCols wraps across lines.
func userColsPrefixed(p string) string {
	parts := strings.Split(userCols, ",")
	for i := range parts {
		parts[i] = p + "." + strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ", ")
}

// ListUserGroupMembers returns the users in a group, newest first.
func (s *Store) ListUserGroupMembers(groupID int64) ([]*User, error) {
	rows, err := s.db.Query(`SELECT `+userColsPrefixed("u")+` FROM users u
		JOIN user_group_members m ON m.user_id = u.id
		WHERE m.group_id=? ORDER BY u.id DESC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserGroupIDsBulk maps user id → group ids for the given users, in one query
// (the admin user list would otherwise issue one query per row).
func (s *Store) UserGroupIDsBulk(userIDs []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	if len(userIDs) == 0 {
		return out, nil
	}
	q := `SELECT user_id, group_id FROM user_group_members WHERE user_id IN (?` +
		strings.Repeat(`,?`, len(userIDs)-1) + `) ORDER BY user_id, group_id`
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid, gid int64
		if err := rows.Scan(&uid, &gid); err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], gid)
	}
	return out, rows.Err()
}

// ---- package ↔ user groups ----

func (s *Store) SetPackageUserGroups(packageID int64, groupIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM package_user_groups WHERE package_id=?`, packageID); err != nil {
		return err
	}
	for _, gid := range groupIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO package_user_groups (package_id, group_id)
			SELECT ?, id FROM user_groups WHERE id=?`, packageID, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) PackageUserGroupIDs(packageID int64) ([]int64, error) {
	return queryInts(s.db, `SELECT group_id FROM package_user_groups WHERE package_id=? ORDER BY group_id`, packageID)
}

// ---- reg code ↔ user groups ----

func (s *Store) SetRegCodeUserGroups(codeID int64, groupIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM reg_code_user_groups WHERE code_id=?`, codeID); err != nil {
		return err
	}
	for _, gid := range groupIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO reg_code_user_groups (code_id, group_id)
			SELECT ?, id FROM user_groups WHERE id=?`, codeID, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RegCodeUserGroupIDs(codeID int64) ([]int64, error) {
	return queryInts(s.db, `SELECT group_id FROM reg_code_user_groups WHERE code_id=? ORDER BY group_id`, codeID)
}

// ---- authorization ----

// rowQuerier is satisfied by both *sql.DB and *sql.Tx, so the buy check can run
// either standalone or inside the purchase transaction.
type rowQuerier interface {
	QueryRow(string, ...any) *sql.Row
}

// CanBuyPackage reports whether userID may buy packageID. A package with no
// user-group bindings is public; otherwise the user must be in at least one
// bound group.
func (s *Store) CanBuyPackage(userID, packageID int64) (bool, error) {
	return canBuyPackageTx(s.db, userID, packageID)
}

func canBuyPackageTx(q rowQuerier, userID, packageID int64) (bool, error) {
	var bound int64
	if err := q.QueryRow(`SELECT COUNT(*) FROM package_user_groups WHERE package_id=?`, packageID).
		Scan(&bound); err != nil {
		return false, err
	}
	if bound == 0 {
		return true, nil // public package
	}
	var hit int64
	if err := q.QueryRow(`SELECT COUNT(*) FROM package_user_groups p
		JOIN user_group_members m ON m.group_id = p.group_id
		WHERE p.package_id=? AND m.user_id=?`, packageID, userID).Scan(&hit); err != nil {
		return false, err
	}
	return hit > 0, nil
}
