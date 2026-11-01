package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
)

// dumpDoc 是写入磁盘前的内存库镜像。
// 之所以不直接用 SQLite 文件镜像：modernc.org/sqlite 的 Deserialize 在
// 内存库上存在分配器不匹配的缺陷（新版本已移除该 API），因此改为
// 「内存库 + 结构化镜像 + 整块加密」，同样保证磁盘上不留明文。
type dumpDoc struct {
	Version  int           `json:"version"`
	Accounts []dumpAccount `json:"accounts"`
	Entries  []dumpEntry   `json:"entries"`
	Meta     []dumpMeta    `json:"meta,omitempty"`
}

// dumpMeta 键值行（旧版库的密码哈希等据此迁移）。
type dumpMeta struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// dumpAccount 账户行。
type dumpAccount struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Currency   string `json:"currency"`
	Note       string `json:"note"`
	Archived   bool   `json:"archived"`
	ArchivedAt string `json:"archivedAt"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// dumpEntry 录入行（金额以分为单位，避免浮点误差）。
type dumpEntry struct {
	ID        int64  `json:"id"`
	AccountID int64  `json:"accountId"`
	Date      string `json:"date"`
	Minor     int64  `json:"amountMinor"`
	Note      string `json:"note"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// dumpAccounts 读出全部账户。
func (s *Store) dumpAccounts(doc *dumpDoc) error {
	rows, err := s.db.Query(`SELECT id, name, category, currency, note, archived,
		COALESCE(archived_at,''), created_at, updated_at FROM accounts ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a dumpAccount
		var archived int
		if err := rows.Scan(&a.ID, &a.Name, &a.Category, &a.Currency, &a.Note, &archived,
			&a.ArchivedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return err
		}
		a.Archived = archived == 1
		doc.Accounts = append(doc.Accounts, a)
	}
	return rows.Err()
}

// dumpEntries 读出全部录入记录。
func (s *Store) dumpEntries(doc *dumpDoc) error {
	rows, err := s.db.Query(`SELECT id, account_id, entry_date, amount_minor, note, created_at, updated_at
		FROM balance_entries ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e dumpEntry
		if err := rows.Scan(&e.ID, &e.AccountID, &e.Date, &e.Minor, &e.Note, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return err
		}
		doc.Entries = append(doc.Entries, e)
	}
	return rows.Err()
}

// dumpMetaRows 读出 meta 键值。
func (s *Store) dumpMetaRows(doc *dumpDoc) error {
	rows, err := s.db.Query(`SELECT key, value FROM meta`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m dumpMeta
		if err := rows.Scan(&m.Key, &m.Value); err != nil {
			return err
		}
		doc.Meta = append(doc.Meta, m)
	}
	return rows.Err()
}

// importLegacy 把旧版明文库（未加密时代）的数据导入内存库。
func (s *Store) importLegacy(path string) error {
	src, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return err
	}
	defer src.Close()

	doc := dumpDoc{Version: dumpVersion}
	accRows, err := src.Query(`SELECT id, name, category, currency, note, archived,
		COALESCE(archived_at,''), created_at, updated_at FROM accounts ORDER BY id`)
	if err != nil {
		return fmt.Errorf("旧版库缺少 accounts 表: %w", err)
	}
	for accRows.Next() {
		var a dumpAccount
		var archived int
		if err := accRows.Scan(&a.ID, &a.Name, &a.Category, &a.Currency, &a.Note, &archived,
			&a.ArchivedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			accRows.Close()
			return err
		}
		a.Archived = archived == 1
		doc.Accounts = append(doc.Accounts, a)
	}
	accRows.Close()
	if err := accRows.Err(); err != nil {
		return err
	}

	entRows, err := src.Query(`SELECT id, account_id, entry_date, amount_minor, note, created_at, updated_at
		FROM balance_entries ORDER BY id`)
	if err != nil {
		return fmt.Errorf("旧版库缺少 balance_entries 表: %w", err)
	}
	for entRows.Next() {
		var e dumpEntry
		if err := entRows.Scan(&e.ID, &e.AccountID, &e.Date, &e.Minor, &e.Note, &e.CreatedAt, &e.UpdatedAt); err != nil {
			entRows.Close()
			return err
		}
		doc.Entries = append(doc.Entries, e)
	}
	entRows.Close()
	if err := entRows.Err(); err != nil {
		return err
	}

	metaRows, err := src.Query(`SELECT key, value FROM meta`)
	if err == nil {
		for metaRows.Next() {
			var m dumpMeta
			if err := metaRows.Scan(&m.Key, &m.Value); err == nil {
				doc.Meta = append(doc.Meta, m)
			}
		}
		metaRows.Close()
		if err := metaRows.Err(); err != nil {
			return err
		}
	}
	return s.loadDump(doc)
}

// loadDump 用镜像重建内存库内容（整体替换）。
func (s *Store) loadDump(doc dumpDoc) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM balance_entries`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM accounts`); err != nil {
		return err
	}

	accStmt, err := tx.Prepare(`INSERT INTO accounts(id, name, category, currency, note, archived, archived_at, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer accStmt.Close()
	for _, a := range doc.Accounts {
		if _, err := accStmt.Exec(a.ID, a.Name, a.Category, a.Currency, a.Note,
			boolInt(a.Archived), nullStr(a.ArchivedAt), a.CreatedAt, a.UpdatedAt); err != nil {
			return err
		}
	}

	entStmt, err := tx.Prepare(`INSERT INTO balance_entries(id, account_id, entry_date, amount_minor, note, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer entStmt.Close()
	for _, e := range doc.Entries {
		if _, err := entStmt.Exec(e.ID, e.AccountID, e.Date, e.Minor, e.Note, e.CreatedAt, e.UpdatedAt); err != nil {
			return err
		}
	}
	for _, m := range doc.Meta {
		if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES(?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, m.Key, m.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// countRows 统计表内行数（用于自检）。
func (s *Store) countRows(table string) (int, error) {
	if table != "accounts" && table != "balance_entries" {
		return 0, errors.New("非法表名")
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
