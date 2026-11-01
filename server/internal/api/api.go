// Package api 实现 fam 的 HTTP 接口层。
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fam/server/internal/model"
	"fam/server/internal/store"
)

// Version 后端版本号。
const Version = "1.0.0"

// Server 聚合存储与静态资源目录。
type Server struct {
	st       *store.Store
	webDir   string
	sessions *sessionStore
}

// New 创建路由。
func New(st *store.Store, webDir string) http.Handler {
	s := &Server{st: st, webDir: webDir, sessions: newSessionStore()}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/meta/categories", s.meta)

	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/setup", s.authSetup)
	mux.HandleFunc("POST /api/auth/login", s.authLogin)
	mux.HandleFunc("POST /api/auth/logout", s.authLogout)
	mux.HandleFunc("POST /api/auth/password", s.authChangePassword)

	mux.HandleFunc("GET /api/accounts", s.listAccounts)
	mux.HandleFunc("POST /api/accounts", s.createAccount)
	mux.HandleFunc("GET /api/accounts/{id}", s.getAccount)
	mux.HandleFunc("PUT /api/accounts/{id}", s.updateAccount)
	mux.HandleFunc("POST /api/accounts/{id}/archive", s.archiveAccount)

	mux.HandleFunc("GET /api/entries", s.listEntries)
	mux.HandleFunc("POST /api/entries", s.createEntries)
	mux.HandleFunc("PUT /api/entries/{id}", s.updateEntry)
	mux.HandleFunc("DELETE /api/entries/{id}", s.deleteEntry)

	mux.HandleFunc("GET /api/stats/summary", s.summary)
	mux.HandleFunc("GET /api/stats/timeseries", s.timeseries)
	mux.HandleFunc("GET /api/stats/compare", s.compare)

	mux.HandleFunc("GET /api/export/entries", s.exportEntries)
	mux.HandleFunc("GET /api/export/summary", s.exportSummary)
	mux.HandleFunc("GET /api/export/timeseries", s.exportTimeseries)

	mux.HandleFunc("GET /api/backup/snapshot", s.snapshot)
	mux.HandleFunc("POST /api/backup/restore", s.restore)
	mux.HandleFunc("POST /api/backup/file", s.backupFile)
	mux.HandleFunc("POST /api/backup/file/restore", s.restoreFile)

	mux.HandleFunc("/", s.static)
	return recoverer(jsonContentType(s.guard(mux)))
}

// errorResponse 统一错误响应体。
type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeJSON 输出 JSON。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail 输出统一错误响应。
func fail(w http.ResponseWriter, status int, code, msg string) {
	var e errorResponse
	e.Error.Code = code
	e.Error.Message = msg
	writeJSON(w, status, e)
}

// badRequest 400 响应。
func badRequest(w http.ResponseWriter, msg string) {
	fail(w, http.StatusBadRequest, "BAD_REQUEST", msg)
}

// serverError 500 响应。
func serverError(w http.ResponseWriter, err error) {
	log.Printf("内部错误: %v", err)
	fail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务器内部错误")
}

// decodeJSON 解析请求体，非法时返回 false 并已写入 400。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		badRequest(w, "请求体为空")
		return false
	}
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		badRequest(w, "请求体不是合法的 JSON："+err.Error())
		return false
	}
	return true
}

// pathID 解析路径中的整数 id。
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		badRequest(w, "路径参数 id 无效")
		return 0, false
	}
	return id, true
}

// mapStoreError 把存储层错误映射为 HTTP 状态。
func mapStoreError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "NOT_FOUND", "记录不存在")
	case errors.Is(err, store.ErrConflict):
		fail(w, http.StatusConflict, "CONFLICT", "同名账户已存在")
	case errors.Is(err, store.ErrInvalidBackup):
		fail(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		if strings.Contains(err.Error(), "日期") || strings.Contains(err.Error(), "快照") ||
			strings.Contains(err.Error(), "备份") || strings.Contains(err.Error(), "恢复模式") ||
			strings.Contains(err.Error(), "路径") || strings.Contains(err.Error(), "目录") {
			fail(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		} else {
			serverError(w, err)
		}
	}
	return true
}

// recoverer 捕获 panic，避免连接中断。
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				fail(w, http.StatusInternalServerError, "INTERNAL_ERROR", fmt.Sprintf("服务器内部错误: %v", rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// jsonContentType 为 /api 请求设置默认响应类型。
func jsonContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		next.ServeHTTP(w, r)
	})
}

// static 提供前端静态文件；未构建前端时返回提示页。
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, http.StatusNotFound, "NOT_FOUND", "接口不存在")
		return
	}
	if s.webDir == "" {
		fail(w, http.StatusNotFound, "NOT_FOUND", "前端资源未配置")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	full := filepath.Join(s.webDir, filepath.FromSlash(name))
	if !strings.HasPrefix(full, filepath.Clean(s.webDir)) {
		fail(w, http.StatusForbidden, "FORBIDDEN", "禁止访问")
		return
	}
	if _, err := os.Stat(full); err != nil {
		if !strings.Contains(name, ".") {
			full = filepath.Join(s.webDir, "index.html")
		} else {
			fail(w, http.StatusNotFound, "NOT_FOUND", "资源不存在")
			return
		}
	}
	// 前端资源要求每次重新校验，避免应用升级后仍加载到旧文件（缓存导致「改了没生效」）
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	http.ServeFile(w, r, full)
}

// health 健康检查。
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": Version,
		"dbPath":  s.st.Path(),
		"time":    model.Today(),
	})
}

// meta 返回类别与币种枚举。
func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"categories":     model.Categories,
		"categoryLabels": model.CategoryLabels,
		"currencies":     model.Currencies,
	})
}
