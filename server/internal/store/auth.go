package store

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"strconv"
)

// 旧版明文库（未加密时代）的密码参数，仅用于一次性迁移校验。
const (
	legacyAuthIterations = 120000
	legacyAuthKeyLength  = 32
)

// meta 键名（旧版）。
const (
	metaAuthHash = "auth_hash"
	metaAuthSalt = "auth_salt"
	metaAuthIter = "auth_iter"
)

// AuthInitialized 旧版库是否已设置访问密码（仅用于迁移判断）。
func (s *Store) AuthInitialized() (bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, metaAuthHash).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v != "", nil
}

// VerifyAuthPassword 按旧版 meta 中的哈希校验密码（仅用于明文库迁移）。
func (s *Store) VerifyAuthPassword(password string) (bool, error) {
	var hashB64, saltB64, iterRaw string
	err := s.db.QueryRow(`SELECT
			COALESCE((SELECT value FROM meta WHERE key = ?), ''),
			COALESCE((SELECT value FROM meta WHERE key = ?), ''),
			COALESCE((SELECT value FROM meta WHERE key = ?), '')`,
		metaAuthHash, metaAuthSalt, metaAuthIter).Scan(&hashB64, &saltB64, &iterRaw)
	if err != nil {
		return false, err
	}
	if hashB64 == "" || saltB64 == "" {
		return false, nil
	}
	want, err := base64.StdEncoding.DecodeString(hashB64)
	if err != nil {
		return false, err
	}
	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return false, err
	}
	iter, err := strconv.Atoi(iterRaw)
	if err != nil || iter <= 0 {
		iter = legacyAuthIterations
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
