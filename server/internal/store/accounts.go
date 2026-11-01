package store

import (
	"database/sql"
	"errors"
	"strings"

	"fam/server/internal/model"
)

const accountColumns = `id, name, category, currency, note, archived, COALESCE(archived_at,''),
	created_at, updated_at`

// AccountFilter 账户列表查询条件。
type AccountFilter struct {
	IncludeArchived bool
	Keyword         string
}

// ListAccounts 返回账户列表，附带每个账户的最新余额与录入日期。
func (s *Store) ListAccounts(f AccountFilter) ([]model.Account, error) {
	q := `SELECT ` + accountColumns + `,
		(SELECT amount_minor FROM balance_entries e WHERE e.account_id = a.id
			ORDER BY e.entry_date DESC, e.id DESC LIMIT 1) AS latest_minor,
		COALESCE((SELECT entry_date FROM balance_entries e WHERE e.account_id = a.id
			ORDER BY e.entry_date DESC, e.id DESC LIMIT 1), '') AS latest_date
		FROM accounts a`
	var where []string
	var args []any
	if !f.IncludeArchived {
		where = append(where, "a.archived = 0")
	}
	if f.Keyword != "" {
		where = append(where, "(a.name LIKE ? OR a.note LIKE ?)")
		kw := "%" + f.Keyword + "%"
		args = append(args, kw, kw)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY a.archived ASC, a.category ASC, a.name ASC"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.Account{}
	for rows.Next() {
		var a model.Account
		var archived int
		var latestMinor sql.NullInt64
		if err := rows.Scan(&a.ID, &a.Name, &a.Category, &a.Currency, &a.Note, &archived,
			&a.ArchivedAt, &a.CreatedAt, &a.UpdatedAt, &latestMinor, &a.LatestDate); err != nil {
			return nil, err
		}
		a.Archived = archived == 1
		if latestMinor.Valid {
			v := FromMinor(latestMinor.Int64)
			a.LatestAmount = &v
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// GetAccount 按 id 查询单个账户。
func (s *Store) GetAccount(id int64) (*model.Account, error) {
	var a model.Account
	var archived int
	err := s.db.QueryRow(`SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id).
		Scan(&a.ID, &a.Name, &a.Category, &a.Currency, &a.Note, &archived,
			&a.ArchivedAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Archived = archived == 1
	return &a, nil
}

// CreateAccount 新增账户；重名（同币种）返回 ErrConflict。
func (s *Store) CreateAccount(a *model.Account) error {
	res, err := s.db.Exec(`INSERT INTO accounts(name, category, currency, note, archived, archived_at, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.Category, a.Currency, a.Note, boolInt(a.Archived), nullStr(a.ArchivedAt), now(), now())
	if err != nil {
		return remapConstraint(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	a.ID = id
	ts := now()
	a.CreatedAt = ts
	a.UpdatedAt = ts
	return s.persist()
}

// UpdateAccount 更新账户字段（零值表示不修改）。
func (s *Store) UpdateAccount(a *model.Account) error {
	ts := now()
	a.UpdatedAt = ts
	res, err := s.db.Exec(`UPDATE accounts SET name=?, category=?, currency=?, note=?, updated_at=? WHERE id=?`,
		a.Name, a.Category, a.Currency, a.Note, ts, a.ID)
	if err != nil {
		return remapConstraint(err)
	}
	if err := ensureAffected(res, 1); err != nil {
		return err
	}
	return s.persist()
}

// SetArchived 归档或恢复账户；归档时写入归档日期。
func (s *Store) SetArchived(id int64, archived bool) error {
	archivedAt := ""
	if archived {
		archivedAt = model.Today()
	}
	res, err := s.db.Exec(`UPDATE accounts SET archived=?, archived_at=?, updated_at=? WHERE id=?`,
		boolInt(archived), nullStr(archivedAt), now(), id)
	if err != nil {
		return err
	}
	if err := ensureAffected(res, 1); err != nil {
		return err
	}
	return s.persist()
}

// ensureAffected 校验影响行数，0 行视为不存在。
func ensureAffected(res sql.Result, want int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != want {
		return ErrNotFound
	}
	return nil
}

// remapConstraint 把唯一约束错误映射为 ErrConflict。
func remapConstraint(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
