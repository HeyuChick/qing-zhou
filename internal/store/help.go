package store

import "time"

type HelpDoc struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	SortOrder int    `json:"sort_order"`
	Published bool   `json:"published"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// ListHelpDocs returns help docs ordered by sort then id. publishedOnly limits
// to published docs (user-facing); admin passes false to see all.
func (s *Store) ListHelpDocs(publishedOnly bool) ([]*HelpDoc, error) {
	q := `SELECT id, title, content, sort_order, published, created_at, updated_at FROM help_docs`
	if publishedOnly {
		q += ` WHERE published=1`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*HelpDoc
	for rows.Next() {
		var d HelpDoc
		if err := rows.Scan(&d.ID, &d.Title, &d.Content, &d.SortOrder, &d.Published, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

func (s *Store) CreateHelpDoc(title, content string, sortOrder int, published bool) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO help_docs (title, content, sort_order, published, created_at, updated_at)
		 VALUES (?,?,?,?,?,?)`, title, content, sortOrder, b2i(published), now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateHelpDoc(id int64, title, content string, sortOrder int, published bool) error {
	_, err := s.db.Exec(
		`UPDATE help_docs SET title=?, content=?, sort_order=?, published=?, updated_at=? WHERE id=?`,
		title, content, sortOrder, b2i(published), time.Now().Unix(), id)
	return err
}

func (s *Store) DeleteHelpDoc(id int64) error {
	_, err := s.db.Exec(`DELETE FROM help_docs WHERE id=?`, id)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
