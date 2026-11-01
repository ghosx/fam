package store

import (
	"database/sql"
	"errors"
	"strings"

	"fam/server/internal/model"
)

// EntryFilter 历史录入查询条件。
type EntryFilter struct {
	AccountID int64
	From      string
	To        string
	Page      int
	PageSize  int
}

// EntryPage 分页结果。
type EntryPage struct {
	Items    []model.Entry `json:"items"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"pageSize"`
}

const entrySelect = `SELECT e.id, e.account_id, a.name, a.category, a.currency, e.entry_date,
	e.amount_minor, e.note, e.created_at, e.updated_at
	FROM balance_entries e JOIN accounts a ON a.id = e.account_id`

// ListEntries 查询历史录入记录，按日期倒序、id 倒序。
func (s *Store) ListEntries(f EntryFilter) (EntryPage, error) {
	var where []string
	var args []any
	if f.AccountID > 0 {
		where = append(where, "e.account_id = ?")
		args = append(args, f.AccountID)
	}
	if f.From != "" {
		where = append(where, "e.entry_date >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "e.entry_date <= ?")
		args = append(args, f.To)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM balance_entries e`+cond, args...).Scan(&total); err != nil {
		return EntryPage{}, err
	}

	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 50
	}
	q := entrySelect + cond + ` ORDER BY e.entry_date DESC, e.id DESC LIMIT ? OFFSET ?`
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return EntryPage{}, err
	}
	defer rows.Close()

	items := []model.Entry{}
	for rows.Next() {
		var e model.Entry
		var minor int64
		if err := rows.Scan(&e.ID, &e.AccountID, &e.AccountName, &e.Category, &e.Currency,
			&e.Date, &minor, &e.Note, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return EntryPage{}, err
		}
		e.Amount = FromMinor(minor)
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return EntryPage{}, err
	}
	return EntryPage{Items: items, Total: total, Page: f.Page, PageSize: f.PageSize}, nil
}

// GetEntry 查询单条录入记录。
func (s *Store) GetEntry(id int64) (*model.Entry, error) {
	var e model.Entry
	var minor int64
	err := s.db.QueryRow(entrySelect+` WHERE e.id = ?`, id).
		Scan(&e.ID, &e.AccountID, &e.AccountName, &e.Category, &e.Currency,
			&e.Date, &minor, &e.Note, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.Amount = FromMinor(minor)
	return &e, nil
}

// UpsertEntries 在同一事务内写入一批录入记录，按 (账户, 日期) 覆盖。
// 校验失败时整体回滚，不会写入部分数据。
func (s *Store) UpsertEntries(date string, items []model.EntryInput) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO balance_entries(account_id, entry_date, amount_minor, note, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, entry_date) DO UPDATE SET
			amount_minor = excluded.amount_minor,
			note = CASE WHEN excluded.note <> '' THEN excluded.note ELSE balance_entries.note END,
			updated_at = excluded.updated_at`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	ts := now()
	for _, it := range items {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM accounts WHERE id = ?`, it.AccountID).Scan(&exists); err != nil {
			return 0, err
		}
		if exists == 0 {
			return 0, ErrNotFound
		}
		if _, err := stmt.Exec(it.AccountID, date, ToMinor(*it.Amount), it.Note, ts, ts); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if err := s.persist(); err != nil {
		return 0, err
	}
	return len(items), nil
}

// UpdateEntry 修改一条历史录入记录。
func (s *Store) UpdateEntry(id int64, accountID int64, date string, amountMinor int64, note string) error {
	res, err := s.db.Exec(`UPDATE balance_entries
		SET account_id=?, entry_date=?, amount_minor=?, note=?, updated_at=? WHERE id=?`,
		accountID, date, amountMinor, note, now(), id)
	if err != nil {
		return remapConstraint(err)
	}
	if err := ensureAffected(res, 1); err != nil {
		return err
	}
	return s.persist()
}

// DeleteEntry 删除一条历史录入记录。
func (s *Store) DeleteEntry(id int64) error {
	res, err := s.db.Exec(`DELETE FROM balance_entries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if err := ensureAffected(res, 1); err != nil {
		return err
	}
	return s.persist()
}

// AllEntriesForExport 导出用：按日期升序返回区间内的全部记录。
func (s *Store) AllEntriesForExport(from, to string) ([]model.Entry, error) {
	q := entrySelect
	var where []string
	var args []any
	if from != "" {
		where = append(where, "e.entry_date >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "e.entry_date <= ?")
		args = append(args, to)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY e.entry_date ASC, e.id ASC"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Entry{}
	for rows.Next() {
		var e model.Entry
		var minor int64
		if err := rows.Scan(&e.ID, &e.AccountID, &e.AccountName, &e.Category, &e.Currency,
			&e.Date, &minor, &e.Note, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.Amount = FromMinor(minor)
		out = append(out, e)
	}
	return out, rows.Err()
}
