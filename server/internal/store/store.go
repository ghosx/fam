// Package store 基于纯 Go 的 SQLite（modernc.org/sqlite）实现 fam 的持久化层。
//
// 安全模型：数据库**只存在于内存中**，落盘的是经过 AES-256-GCM 加密的整库镜像
// （见 internal/vault）。未解锁时内存里没有数据，磁盘上也没有明文，
// 直接读取 fam.db 只能看到密文。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"fam/server/internal/vault"
)

// 当前库结构版本，用于恢复时的兼容性校验。
const schemaVersion = 1

// 数据镜像版本。
const dumpVersion = 1

// 错误定义。
var (
	// ErrNotFound 表示目标记录不存在。
	ErrNotFound = errors.New("记录不存在")
	// ErrConflict 表示唯一约束冲突。
	ErrConflict = errors.New("记录已存在")
	// ErrInvalidBackup 表示备份/恢复文件不合法（HTTP 层映射为 400）。
	ErrInvalidBackup = errors.New("备份文件无效")
	// ErrLocked 表示数据库尚未解锁。
	ErrLocked = errors.New("数据尚未解锁")
	// ErrAlreadyInitialized 表示已经设置过访问密码。
	ErrAlreadyInitialized = errors.New("访问密码已设置")
)

// Store 持有一个内存中的 SQLite 连接与加密容器。
type Store struct {
	mu       sync.Mutex
	db       *sql.DB
	path     string
	v        *vault.Vault
	unlocked bool
	// legacy 表示内存中加载的是旧版明文库，等待设置/校验密码后加密迁移
	legacy bool
}

// Path 返回加密数据文件路径。
func (s *Store) Path() string { return s.path }

// DB 暴露底层连接（仅供测试与兼容用途）。
func (s *Store) DB() *sql.DB { return s.db }

// openMem 创建内存数据库连接。单连接保证 Serialize/Deserialize 与数据一致。
func openMem() (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:fam?mode=memory&cache=private&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("打开内存数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接内存数据库失败: %w", err)
	}
	return db, nil
}

// Open 打开数据文件。此时处于锁定状态，需要 Setup 或 Unlock 后才能读写数据。
// 若检测到旧版明文数据库，会先载入内存，等设置/校验密码后自动加密迁移。
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("数据库路径为空")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录失败: %w", err)
		}
	}
	db, err := openMem()
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	// 旧版明文库迁移检测：文件存在但不是加密容器
	if !vault.Exists(path) && IsSQLiteFile(path) {
		if err := s.importLegacy(path); err != nil {
			db.Close()
			return nil, fmt.Errorf("载入旧版数据失败: %w", err)
		}
		if err := s.migrate(); err != nil {
			db.Close()
			return nil, err
		}
		s.legacy = true
	}
	return s, nil
}

// Close 关闭连接（已解锁时先落盘）。
func (s *Store) Close() error {
	if s.unlocked {
		_ = s.persist()
	}
	return s.db.Close()
}

// serialize 把内存库导出为可持久化的镜像（JSON）。
func (s *Store) serialize() ([]byte, error) {
	doc := dumpDoc{Version: dumpVersion}
	if err := s.dumpAccounts(&doc); err != nil {
		return nil, err
	}
	if err := s.dumpEntries(&doc); err != nil {
		return nil, err
	}
	return json.Marshal(doc)
}

// deserialize 用镜像替换内存库内容。
func (s *Store) deserialize(body []byte) error {
	var doc dumpDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("解析数据镜像失败: %w", err)
	}
	if doc.Version != dumpVersion {
		return fmt.Errorf("数据镜像版本不支持: %d", doc.Version)
	}
	return s.loadDump(doc)
}

// persist 把内存库加密写盘。
func (s *Store) persist() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

// Flush 立即把内存数据加密落盘（供测试与关盘前显式调用）。
func (s *Store) Flush() error { return s.persist() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			category TEXT NOT NULL,
			currency TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			archived INTEGER NOT NULL DEFAULT 0,
			archived_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(name, currency)
		)`,
		`CREATE TABLE IF NOT EXISTS balance_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			entry_date TEXT NOT NULL,
			amount_minor INTEGER NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(account_id, entry_date)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_entries_date ON balance_entries(entry_date)`,
		`CREATE INDEX IF NOT EXISTS idx_entries_account ON balance_entries(account_id)`,
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(st); err != nil {
			return fmt.Errorf("执行迁移失败: %w", err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES('schema_version', ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(schemaVersion)); err != nil {
		return fmt.Errorf("写入版本信息失败: %w", err)
	}
	return nil
}

// ToMinor 把十进制金额转换为以「分」为单位的整数（四舍五入，远离零方向）。
func ToMinor(amount float64) int64 {
	if amount < 0 {
		return -int64(math.Floor(-amount*100 + 0.5))
	}
	return int64(math.Floor(amount*100 + 0.5))
}

// FromMinor 把「分」转换回十进制金额。
func FromMinor(minor int64) float64 { return float64(minor) / 100.0 }

// FormatAmount 输出两位小数的金额字符串，用于 CSV 导出。
func FormatAmount(minor int64) string {
	neg := minor < 0
	if neg {
		minor = -minor
	}
	s := strconv.FormatInt(minor/100, 10) + "." + fmt.Sprintf("%02d", minor%100)
	if neg {
		s = "-" + s
	}
	return s
}

// IsSQLiteFile 判断是否为明文 SQLite 文件（用于识别旧版数据）。
func IsSQLiteFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 16)
	n, _ := f.Read(buf)
	return n == 16 && strings.HasPrefix(string(buf[:n]), "SQLite format 3\x00")
}

// now 返回当前时间的统一字符串格式。
func now() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
