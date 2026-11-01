package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"fam/server/internal/model"
	"fam/server/internal/vault"
)

// Snapshot 导出全量 JSON 快照（明文导出，由用户自行保管）。
func (s *Store) Snapshot() (model.Snapshot, error) {
	snap := model.Snapshot{
		Version:    model.SnapshotVersion,
		ExportedAt: time.Now().Format("2006-01-02 15:04:05"),
		Accounts:   []model.SnapshotAcc{},
		Entries:    []model.SnapshotEntry{},
	}
	rows, err := s.db.Query(`SELECT name, category, currency, note, archived, COALESCE(archived_at,'')
		FROM accounts ORDER BY id`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var a model.SnapshotAcc
		var archived int
		if err := rows.Scan(&a.Name, &a.Category, &a.Currency, &a.Note, &archived, &a.ArchivedAt); err != nil {
			return snap, err
		}
		a.Archived = archived == 1
		snap.Accounts = append(snap.Accounts, a)
	}
	if err := rows.Err(); err != nil {
		return snap, err
	}

	er, err := s.db.Query(`SELECT a.name, a.currency, e.entry_date, e.amount_minor, e.note
		FROM balance_entries e JOIN accounts a ON a.id = e.account_id
		ORDER BY e.entry_date ASC, e.id ASC`)
	if err != nil {
		return snap, err
	}
	defer er.Close()
	for er.Next() {
		var e model.SnapshotEntry
		var minor int64
		if err := er.Scan(&e.AccountName, &e.Currency, &e.Date, &minor, &e.Note); err != nil {
			return snap, err
		}
		e.Amount = FromMinor(minor)
		snap.Entries = append(snap.Entries, e)
	}
	return snap, er.Err()
}

// RestoreSnapshot 从快照恢复数据，mode 为 replace（覆盖）或 merge（合并）。
func (s *Store) RestoreSnapshot(snap model.Snapshot, mode string) error {
	if snap.Version != model.SnapshotVersion {
		return fmt.Errorf("快照版本不兼容：%d（当前支持 %d）", snap.Version, model.SnapshotVersion)
	}
	if snap.Accounts == nil || snap.Entries == nil {
		return errors.New("快照数据不完整：缺少 accounts 或 entries")
	}
	for _, a := range snap.Accounts {
		if a.Name == "" || !model.IsCategory(a.Category) || !model.IsCurrency(a.Currency) {
			return fmt.Errorf("快照含非法账户数据：%+v", a)
		}
	}
	for _, e := range snap.Entries {
		if !model.ValidDate(e.Date) {
			return errors.New("快照含非法日期：" + e.Date)
		}
	}
	if mode != "replace" && mode != "merge" {
		return errors.New("恢复模式必须为 replace 或 merge")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	idOf := map[string]int64{}
	if mode == "replace" {
		if _, err := tx.Exec(`DELETE FROM balance_entries`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM accounts`); err != nil {
			return err
		}
	} else {
		rows, err := tx.Query(`SELECT id, name, currency FROM accounts`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var name, cur string
			if err := rows.Scan(&id, &name, &cur); err != nil {
				rows.Close()
				return err
			}
			idOf[name+"\x00"+cur] = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}

	ts := now()
	for _, a := range snap.Accounts {
		key := a.Name + "\x00" + a.Currency
		if id, ok := idOf[key]; ok {
			if _, err := tx.Exec(`UPDATE accounts SET note=?, updated_at=? WHERE id=?`, a.Note, ts, id); err != nil {
				return err
			}
			continue
		}
		res, err := tx.Exec(`INSERT INTO accounts(name, category, currency, note, archived, archived_at, created_at, updated_at)
			VALUES(?,?,?,?,?,?,?,?)`, a.Name, a.Category, a.Currency, a.Note,
			boolInt(a.Archived), nullStr(a.ArchivedAt), ts, ts)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		idOf[key] = id
	}

	stmt, err := tx.Prepare(`INSERT INTO balance_entries(account_id, entry_date, amount_minor, note, created_at, updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(account_id, entry_date) DO UPDATE SET
			amount_minor=excluded.amount_minor, note=excluded.note, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range snap.Entries {
		id, ok := idOf[e.AccountName+"\x00"+e.Currency]
		if !ok {
			return fmt.Errorf("快照中的录入记录找不到对应账户：%s", e.AccountName)
		}
		if _, err := stmt.Exec(id, e.Date, ToMinor(e.Amount), e.Note, ts, ts); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.persist()
}

// BackupToFile 生成加密备份：先把内存数据落盘，再把容器文件复制到目标目录。
// 备份文件本身就是密文，必须用同一密码才能恢复。
func (s *Store) BackupToFile(dir string) (string, error) {
	if !s.unlocked {
		return "", ErrLocked
	}
	if err := s.persist(); err != nil {
		return "", err
	}
	if dir == "" {
		dir = filepath.Dir(s.path)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	base := fmt.Sprintf("fam-%s", time.Now().Format("20060102-150405"))
	target := filepath.Join(dir, base+".db")
	if _, err := os.Stat(target); err == nil {
		target = filepath.Join(dir, fmt.Sprintf("%s-%d.db", base, time.Now().Nanosecond()%100000))
	}
	if err := copyFile(s.path, target); err != nil {
		return "", fmt.Errorf("写入备份失败: %w", err)
	}
	return target, nil
}

// RestoreFromFile 用加密备份替换当前数据。
// 备份必须能与当前口令解密（即同一密码创建）；恢复后当前口令保持不变。
func (s *Store) RestoreFromFile(src string) error {
	if !s.unlocked || s.v == nil {
		return ErrLocked
	}
	if src == "" {
		return errors.New("备份文件路径为空")
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if !vault.Exists(abs) {
		return fmt.Errorf("%w: 文件不存在或不是 fam 加密备份", ErrInvalidBackup)
	}
	bv, body, err := vault.OpenWithKEK(abs, s.v.KEK())
	if err != nil {
		if errors.Is(err, vault.ErrWrongPassword) {
			return fmt.Errorf("%w: 无法解密该备份，它可能是用其他密码创建的", ErrInvalidBackup)
		}
		return fmt.Errorf("%w: %v", ErrInvalidBackup, err)
	}
	// 采用备份的数据密钥，保留当前 KEK（当前密码继续有效）
	if err := s.v.Import(bv.DEK(), body); err != nil {
		return err
	}
	if err := s.deserialize(body); err != nil {
		return vault.ErrCorrupt
	}
	if err := s.migrate(); err != nil {
		return err
	}
	return s.persist()
}

// copyFile 复制文件内容。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
