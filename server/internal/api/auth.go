package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"fam/server/internal/store"
	"fam/server/internal/vault"
)

// 会话与限流参数。
const (
	sessionTTL      = 12 * time.Hour
	maxLoginFails   = 5
	loginLockWindow = 10 * time.Minute
	loginLockPeriod = 60 * time.Second
	minPasswordLen  = 6
	maxPasswordLen  = 72
)

// 公开接口（无需登录）。
var publicPaths = map[string]bool{
	"/api/health":      true,
	"/api/auth/status": true,
	"/api/auth/setup":  true,
	"/api/auth/login":  true,
}

// sessionStore 内存会话表，随进程生命周期存在。
type sessionStore struct {
	mu    sync.Mutex
	items map[string]time.Time
	fails map[string]*failState
}

// failState 单个客户端的失败计数。
type failState struct {
	count    int
	firstAt  time.Time
	lockTill time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{items: map[string]time.Time{}, fails: map[string]*failState{}}
}

// create 生成新会话令牌。
func (s *sessionStore) create() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[token] = time.Now().Add(sessionTTL)
	return token, nil
}

// valid 判断令牌是否有效（顺带清理过期项）。
func (s *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	till, ok := s.items[token]
	if !ok {
		return false
	}
	if time.Now().After(till) {
		delete(s.items, token)
		return false
	}
	return true
}

// revoke 使单个令牌失效。
func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, token)
}

// revokeAll 使全部令牌失效（锁定或改密后使用）。
func (s *sessionStore) revokeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = map[string]time.Time{}
}

// allowAttempt 判断该客户端是否允许尝试登录。
func (s *sessionStore) allowAttempt(key string) (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.fails[key]
	if !ok {
		return true, 0
	}
	now := time.Now()
	if now.Before(st.lockTill) {
		return false, int(st.lockTill.Sub(now).Seconds()) + 1
	}
	if now.Sub(st.firstAt) > loginLockWindow {
		delete(s.fails, key)
	}
	return true, 0
}

// recordFail 记录一次失败，返回是否已触发锁定。
func (s *sessionStore) recordFail(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.fails[key]
	now := time.Now()
	if !ok || now.Sub(st.firstAt) > loginLockWindow {
		st = &failState{firstAt: now}
		s.fails[key] = st
	}
	st.count++
	if st.count >= maxLoginFails {
		st.lockTill = now.Add(loginLockPeriod)
		return true
	}
	return false
}

// resetFails 登录成功后清除失败计数。
func (s *sessionStore) resetFails(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fails, key)
}

// clientKey 以客户端地址作为限流键（本机应用，无代理场景）。
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// tokenOf 从请求头提取令牌。
func tokenOf(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if v := r.Header.Get("X-Fam-Token"); v != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

// guard 对除公开接口外的 /api 请求做「已初始化 + 已解锁 + 会话有效」校验。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if !s.st.Initialized() {
			fail(w, http.StatusUnauthorized, "NOT_INITIALIZED", "尚未设置访问密码")
			return
		}
		if !s.st.Unlocked() {
			fail(w, http.StatusUnauthorized, "LOCKED", "数据未解锁，请先输入访问密码")
			return
		}
		if !s.sessions.valid(tokenOf(r)) {
			fail(w, http.StatusUnauthorized, "UNAUTHORIZED", "登录状态已失效，请重新解锁")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authStatus GET /api/auth/status
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	initialized := s.st.Initialized()
	unlocked := s.st.Unlocked()
	writeJSON(w, http.StatusOK, map[string]any{
		"initialized":   initialized,
		"unlocked":      unlocked,
		"authenticated": initialized && unlocked && s.sessions.valid(tokenOf(r)),
		"encrypted":     true,
	})
}

// validatePassword 校验密码强度（长度）。
func validatePassword(pw string) string {
	n := len([]rune(pw))
	if n < minPasswordLen {
		return "密码至少需要 " + strconv.Itoa(minPasswordLen) + " 个字符"
	}
	if n > maxPasswordLen {
		return "密码不能超过 " + strconv.Itoa(maxPasswordLen) + " 个字符"
	}
	if strings.TrimSpace(pw) == "" {
		return "密码不能全为空格"
	}
	return ""
}

// authSetup POST /api/auth/setup —— 首次设置密码（同时生成加密密钥）并直接登录
func (s *Server) authSetup(w http.ResponseWriter, r *http.Request) {
	if s.st.Initialized() {
		fail(w, http.StatusConflict, "ALREADY_INITIALIZED", "访问密码已设置，请直接解锁")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		badRequest(w, msg)
		return
	}
	if err := s.st.Setup(req.Password); err != nil {
		if errors.Is(err, store.ErrAlreadyInitialized) {
			fail(w, http.StatusConflict, "ALREADY_INITIALIZED", "访问密码已设置，请直接解锁")
			return
		}
		serverError(w, err)
		return
	}
	token, err := s.sessions.create()
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "initialized": true, "encrypted": true})
}

// authLogin POST /api/auth/login
func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	if !s.st.Initialized() {
		fail(w, http.StatusUnauthorized, "NOT_INITIALIZED", "尚未设置访问密码")
		return
	}
	key := clientKey(r)
	if ok, wait := s.sessions.allowAttempt(key); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		fail(w, http.StatusTooManyRequests, "TOO_MANY_ATTEMPTS", "尝试次数过多，请稍后再试")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.st.Unlock(req.Password); err != nil {
		switch {
		case errors.Is(err, vault.ErrWrongPassword):
			if s.sessions.recordFail(key) {
				w.Header().Set("Retry-After", strconv.Itoa(int(loginLockPeriod.Seconds())))
				fail(w, http.StatusTooManyRequests, "TOO_MANY_ATTEMPTS", "尝试次数过多，请稍后再试")
				return
			}
			fail(w, http.StatusUnauthorized, "BAD_PASSWORD", "密码不正确")
		case errors.Is(err, vault.ErrCorrupt):
			fail(w, http.StatusBadRequest, "CORRUPT", "数据文件已损坏或被篡改，无法解锁")
		default:
			serverError(w, err)
		}
		return
	}
	s.sessions.resetFails(key)
	token, err := s.sessions.create()
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "unlocked": true})
}

// authLogout POST /api/auth/logout —— 注销会话并锁定数据（清空内存中的明文）
func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Lock(); err != nil {
		serverError(w, err)
		return
	}
	s.sessions.revokeAll()
	writeJSON(w, http.StatusOK, map[string]any{"loggedOut": true, "locked": true})
}

// authChangePassword POST /api/auth/password
func (s *Server) authChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := validatePassword(req.NewPassword); msg != "" {
		badRequest(w, msg)
		return
	}
	if err := s.st.ChangePassword(req.OldPassword, req.NewPassword); err != nil {
		if errors.Is(err, vault.ErrWrongPassword) {
			fail(w, http.StatusUnauthorized, "BAD_PASSWORD", "原密码不正确")
			return
		}
		serverError(w, err)
		return
	}
	// 改密后所有会话失效，签发新令牌返回，避免当前窗口被踢出
	s.sessions.revokeAll()
	token, err := s.sessions.create()
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "changed": true})
}
