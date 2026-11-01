package store

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// legacyDB 在指定路径创建一个旧版明文库。
func legacyDB(t *testing.T, path string, rows int, password string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, category TEXT NOT NULL,
			currency TEXT NOT NULL, note TEXT NOT NULL DEFAULT '', archived INTEGER NOT NULL DEFAULT 0,
			archived_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(name, currency))`,
		`CREATE TABLE balance_entries (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL,
			entry_date TEXT NOT NULL, amount_minor INTEGER NOT NULL, note TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(account_id, entry_date))`,
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < rows; i++ {
		if _, err := db.Exec(`INSERT INTO accounts(name,category,currency,note,archived,created_at,updated_at)
			VALUES(?, 'bank','CNY','',0,'2024-01-01 00:00:00','2024-01-01 00:00:00')`, fmt.Sprintf("旧账户%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO balance_entries(account_id,entry_date,amount_minor,note,created_at,updated_at)
		VALUES(1,'2024-01-01',123456,'旧备注','2024-01-01 00:00:00','2024-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	if password != "" {
		salt := []byte("0123456789abcdef")
		key, err := pbkdf2.Key(sha256.New, password, salt, 120000, 32)
		if err != nil {
			t.Fatal(err)
		}
		items := map[string]string{
			"auth_hash": base64.StdEncoding.EncodeToString(key),
			"auth_salt": base64.StdEncoding.EncodeToString(salt),
			"auth_iter": "120000",
		}
		for k, v := range items {
			if _, err := db.Exec(`INSERT INTO meta(key,value) VALUES(?,?)`, k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// M06 旧版明文库（未设密码）→ 设置密码后自动加密，明文文件被清除
func TestM06_LegacyMigrateOnSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fam.db")
	legacyDB(t, path, 2, "")
	if !IsSQLiteFile(path) {
		t.Fatalf("前置条件：应存在明文库")
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.Initialized() {
		t.Fatalf("旧库未设密码，不应判定为已初始化")
	}
	if err := st.Setup("pass123456"); err != nil {
		t.Fatalf("设置密码失败: %v", err)
	}
	// 旧数据保留
	if n, _ := st.countRows("accounts"); n != 2 {
		t.Fatalf("迁移后账户数应为 2，实际 %d", n)
	}
	// 文件已变成密文
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[:8]) != "FAMVAULT" {
		t.Fatalf("迁移后应为加密容器，实际 %q", string(raw[:8]))
	}
	if IsSQLiteFile(path) {
		t.Fatalf("迁移后不应再是明文 SQLite 文件")
	}
}

// M07 旧版明文库（已设密码）→ 用原密码解锁并加密迁移
func TestM07_LegacyMigrateOnUnlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fam.db")
	legacyDB(t, path, 1, "legacy-pass-1")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if !st.Initialized() {
		t.Fatalf("带密码的旧库应判定为已初始化")
	}
	if st.Unlocked() {
		t.Fatalf("旧库打开后应是锁定状态")
	}
	if err := st.Unlock("wrong-pass"); err == nil {
		t.Fatalf("错误密码不应解锁旧库")
	}
	if err := st.Unlock("legacy-pass-1"); err != nil {
		t.Fatalf("原密码应可解锁: %v", err)
	}
	if n, _ := st.countRows("accounts"); n != 1 {
		t.Fatalf("迁移后账户数应为 1，实际 %d", n)
	}
	if IsSQLiteFile(path) {
		t.Fatalf("迁移后不应残留明文库")
	}
	// 关闭后用同一密码重新打开
	st.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if !st2.Initialized() || st2.Unlocked() {
		t.Fatalf("重启后应为「已初始化且锁定」")
	}
	if err := st2.Unlock("legacy-pass-1"); err != nil {
		t.Fatalf("迁移后原密码应继续可用: %v", err)
	}
}

// 这组用例验证「内存库 + 结构化镜像 + 加密落盘」的核心行为。

// M01 空库镜像可往返
func TestM01_EmptyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fam.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Setup("pass123456"); err != nil {
		t.Fatal(err)
	}
	body, err := st.serialize()
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if len(body) == 0 {
		t.Fatalf("导出内容为空")
	}
	if err := st.deserialize(body); err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	n, err := st.countRows("accounts")
	if err != nil || n != 0 {
		t.Fatalf("空库行数应为 0: %d %v", n, err)
	}
}

// M02 账户与录入可完整往返（含 id、时间戳、负金额）
func TestM02_DataRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fam.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Setup("pass123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO accounts(name,category,currency,note,archived,created_at,updated_at)
		VALUES('招商银行','bank','CNY','工资卡',1,'2024-01-01 00:00:00','2024-01-02 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO balance_entries(account_id,entry_date,amount_minor,note,created_at,updated_at)
		VALUES(1,'2024-02-01',-123456,'备注','2024-02-01 00:00:00','2024-02-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	body, err := st.serialize()
	if err != nil {
		t.Fatal(err)
	}

	// 清空后重新导入
	if _, err := st.DB().Exec(`DELETE FROM balance_entries; DELETE FROM accounts`); err != nil {
		t.Fatal(err)
	}
	if err := st.deserialize(body); err != nil {
		t.Fatalf("导入失败: %v", err)
	}

	var name, note, archivedAt string
	var archived int
	if err := st.DB().QueryRow(`SELECT name, note, archived FROM accounts WHERE id = 1`).
		Scan(&name, &note, &archived); err != nil {
		t.Fatalf("账户未恢复: %v", err)
	}
	if name != "招商银行" || note != "工资卡" || archived != 1 {
		t.Fatalf("账户内容不正确: %s %s %d", name, note, archived)
	}
	var minor int64
	if err := st.DB().QueryRow(`SELECT amount_minor FROM balance_entries WHERE id = 1`).Scan(&minor); err != nil {
		t.Fatalf("录入未恢复: %v", err)
	}
	if minor != -123456 {
		t.Fatalf("金额不正确: %d", minor)
	}
	_ = archivedAt
}

// M03 重新导入会替换原有数据（不残留旧行）
func TestM03_ImportReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fam.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Setup("pass123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO accounts(name,category,currency,note,archived,created_at,updated_at)
		VALUES('旧账户','bank','CNY','',0,'x','x')`); err != nil {
		t.Fatal(err)
	}
	body, _ := st.serialize()

	if _, err := st.DB().Exec(`INSERT INTO accounts(name,category,currency,note,archived,created_at,updated_at)
		VALUES('新账户','bank','CNY','',0,'x','x')`); err != nil {
		t.Fatal(err)
	}
	if err := st.deserialize(body); err != nil {
		t.Fatal(err)
	}
	n, _ := st.countRows("accounts")
	if n != 1 {
		t.Fatalf("导入后应只有 1 行，实际 %d", n)
	}
	var name string
	_ = st.DB().QueryRow(`SELECT name FROM accounts`).Scan(&name)
	if name != "旧账户" {
		t.Fatalf("导入未替换数据: %s", name)
	}
}

// M04 镜像损坏时报错而非静默丢数据
func TestM04_BadDump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fam.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Setup("pass123456"); err != nil {
		t.Fatal(err)
	}
	if err := st.deserialize([]byte("{not json")); err == nil {
		t.Fatalf("非法镜像应返回错误")
	}
	if err := st.deserialize([]byte(`{"version":99,"accounts":[],"entries":[]}`)); err == nil {
		t.Fatalf("不支持的版本应返回错误")
	}
}

// M05 锁定后内存中无数据
func TestM05_LockDropsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fam.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Setup("pass123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO accounts(name,category,currency,note,archived,created_at,updated_at)
		VALUES('账户','bank','CNY','',0,'x','x')`); err != nil {
		t.Fatal(err)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := st.Lock(); err != nil {
		t.Fatalf("锁定失败: %v", err)
	}
	if st.Unlocked() {
		t.Fatalf("锁定后 Unlocked 应为 false")
	}
	n, err := st.countRows("accounts")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("锁定后内存中不应有数据，实际 %d 行", n)
	}
	if err := st.Unlock("pass123456"); err != nil {
		t.Fatalf("解锁失败: %v", err)
	}
	if n, _ := st.countRows("accounts"); n != 1 {
		t.Fatalf("解锁后数据应恢复，实际 %d 行", n)
	}
}
