package store

import (
	"errors"
	"time"
)

var (
	ErrUserNotFound      = errors.New("用户不存在")
	ErrNegativeBalance   = errors.New("积分余额不足，无法扣减")
	ErrInsufficientFunds = errors.New("积分不足")
)

type PointTx struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"user_id"`
	Amount       int64  `json:"amount"`
	Type         string `json:"type"`
	BalanceAfter int64  `json:"balance_after"`
	RefID        int64  `json:"ref_id"`
	Note         string `json:"note"`
	OperatorID   int64  `json:"operator_id"`
	CreatedAt    int64  `json:"created_at"`
}

// AdjustPoints credits (amount>0) or debits (amount<0) a user's points and
// writes a ledger row, atomically. Returns the new balance. Balance may not go
// negative. txType is e.g. "admin_recharge" or "adjust".
func (s *Store) AdjustPoints(userID, amount int64, txType string, operatorID int64, note string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var balance int64
	err = tx.QueryRow(`SELECT points FROM users WHERE id=?`, userID).Scan(&balance)
	if err != nil {
		return 0, ErrUserNotFound
	}
	newBalance := balance + amount
	if newBalance < 0 {
		return 0, ErrNegativeBalance
	}
	now := time.Now().Unix()
	if _, err = tx.Exec(`UPDATE users SET points=?, updated_at=? WHERE id=?`, newBalance, now, userID); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(`INSERT INTO point_transactions
		(user_id, amount, type, balance_after, ref_id, note, operator_id, created_at)
		VALUES (?,?,?,?,0,?,?,?)`,
		userID, amount, txType, newBalance, note, operatorID, now); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return newBalance, nil
}

func (s *Store) ListTransactions(userID int64, limit int) ([]*PointTx, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, user_id, amount, type, balance_after, ref_id, note, operator_id, created_at
		FROM point_transactions WHERE user_id=? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PointTx
	for rows.Next() {
		var p PointTx
		if err := rows.Scan(&p.ID, &p.UserID, &p.Amount, &p.Type, &p.BalanceAfter,
			&p.RefID, &p.Note, &p.OperatorID, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}
