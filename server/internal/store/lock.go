package store

import (
	"os"

	"fam/server/internal/vault"
)

// Initialized 判断是否已经设置过访问密码（存在加密容器，或存在带密码的旧版库）。
func (s *Store) Initialized() bool {
	if vault.Exists(s.path) {
		return true
	}
	if s.legacy {
		ok, err := s.AuthInitialized()
		return err == nil && ok
	}
	return false
}

// Unlocked 判断数据是否已解锁（内存中是否载入了数据）。
func (s *Store) Unlocked() bool { return s.unlocked }

// Setup 首次设置密码：用当前内存镜像创建加密容器并解锁。
func (s *Store) Setup(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Initialized() {
		return ErrAlreadyInitialized
	}
	body, err := s.serialize()
	if err != nil {
		return err
	}
	v, err := vault.Create(s.path, password, body)
	if err != nil {
		return err
	}
	s.v = v
	s.unlocked = true
	if s.legacy {
		s.legacy = false
		s.removeLegacyPlaintext()
	}
	return nil
}

// Unlock 用密码解锁数据；若是旧版明文库则同时完成加密迁移。
// 已解锁时同样会校验密码，避免「已解锁就接受任意密码」。
func (s *Store) Unlock(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unlocked && s.v != nil {
		if !s.v.Verify(password) {
			return vault.ErrWrongPassword
		}
		return nil
	}
	if vault.Exists(s.path) {
		v, body, err := vault.Open(s.path, password)
		if err != nil {
			return err
		}
		if err := s.deserialize(body); err != nil {
			return vault.ErrCorrupt
		}
		if err := s.migrate(); err != nil {
			return err
		}
		s.v = v
		s.unlocked = true
		return nil
	}
	if s.legacy {
		// 旧版库：按原有 meta.auth_hash 校验密码，然后加密迁移
		ok, err := s.VerifyAuthPassword(password)
		if err != nil {
			return err
		}
		if !ok {
			return vault.ErrWrongPassword
		}
		body, err := s.serialize()
		if err != nil {
			return err
		}
		v, err := vault.Create(s.path, password, body)
		if err != nil {
			return err
		}
		s.v = v
		s.unlocked = true
		s.legacy = false
		s.removeLegacyPlaintext()
		return nil
	}
	return vault.ErrNotInitialized
}

// Lock 锁定：落盘后清空内存中的数据。
func (s *Store) Lock() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.unlocked {
		return nil
	}
	if err := s.persistLocked(); err != nil {
		return err
	}
	db, err := openMem()
	if err != nil {
		return err
	}
	old := s.db
	s.db = db
	if err := s.migrate(); err != nil {
		db.Close()
		s.db = old
		return err
	}
	old.Close()
	s.v = nil
	s.unlocked = false
	return nil
}

// VerifyPassword 校验当前密码是否正确（不改变解锁状态）。
func (s *Store) VerifyPassword(password string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.v == nil {
		return false
	}
	return s.v.Verify(password)
}

// ChangePassword 修改访问密码：重新包裹数据密钥并落盘。
func (s *Store) ChangePassword(oldPassword, newPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.unlocked || s.v == nil {
		return ErrLocked
	}
	if !s.v.Verify(oldPassword) {
		return vault.ErrWrongPassword
	}
	body, err := s.serialize()
	if err != nil {
		return err
	}
	return s.v.ChangePassword(newPassword, body)
}

// persistLocked 在已持有锁的情况下落盘。
func (s *Store) persistLocked() error {
	if !s.unlocked || s.v == nil {
		return nil
	}
	body, err := s.serialize()
	if err != nil {
		return err
	}
	return s.v.Save(body)
}

// removeLegacyPlaintext 清理旧版明文库残留的 WAL 文件。
// 注意：s.path 本身此时已经被加密容器覆盖，不能再删除。
func (s *Store) removeLegacyPlaintext() {
	for _, p := range []string{s.path + "-wal", s.path + "-shm"} {
		_ = os.Remove(p)
	}
}
