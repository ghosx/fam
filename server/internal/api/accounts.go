package api

import (
	"net/http"
	"strings"

	"fam/server/internal/model"
	"fam/server/internal/store"
)

// accountRequest 账户新增/编辑请求体。
type accountRequest struct {
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Currency string  `json:"currency"`
	Note     *string `json:"note"`
}

// validateName 校验账户名称。
func validateName(name string) (string, string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "账户名称不能为空"
	}
	if len([]rune(name)) > 50 {
		return "", "账户名称不能超过 50 个字符"
	}
	return name, ""
}

// validateNote 校验备注长度。
func validateNote(note string) (string, string) {
	if len([]rune(note)) > 200 {
		return "", "备注不能超过 200 个字符"
	}
	return strings.TrimSpace(note), ""
}

// listAccounts GET /api/accounts
func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := s.st.ListAccounts(store.AccountFilter{
		IncludeArchived: q.Get("includeArchived") == "true",
		Keyword:         strings.TrimSpace(q.Get("keyword")),
	})
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": len(list)})
}

// createAccount POST /api/accounts
func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	var req accountRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, msg := validateName(req.Name)
	if msg != "" {
		badRequest(w, msg)
		return
	}
	if !model.IsCategory(req.Category) {
		badRequest(w, "账户类别不合法")
		return
	}
	if !model.IsCurrency(req.Currency) {
		badRequest(w, "币种不合法")
		return
	}
	note := ""
	if req.Note != nil {
		note, msg = validateNote(*req.Note)
		if msg != "" {
			badRequest(w, msg)
			return
		}
	}
	a := &model.Account{Name: name, Category: req.Category, Currency: req.Currency, Note: note}
	if err := s.st.CreateAccount(a); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// getAccount GET /api/accounts/{id}
func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, err := s.st.GetAccount(id)
	if mapStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// updateAccount PUT /api/accounts/{id}（空字段表示不修改）
func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cur, err := s.st.GetAccount(id)
	if mapStoreError(w, err) {
		return
	}
	var req accountRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	out := *cur
	if req.Name != "" {
		name, msg := validateName(req.Name)
		if msg != "" {
			badRequest(w, msg)
			return
		}
		out.Name = name
	}
	if req.Category != "" {
		if !model.IsCategory(req.Category) {
			badRequest(w, "账户类别不合法")
			return
		}
		out.Category = req.Category
	}
	if req.Currency != "" {
		if !model.IsCurrency(req.Currency) {
			badRequest(w, "币种不合法")
			return
		}
		out.Currency = req.Currency
	}
	if req.Note != nil {
		note, msg := validateNote(*req.Note)
		if msg != "" {
			badRequest(w, msg)
			return
		}
		out.Note = note
	}
	if err := s.st.UpdateAccount(&out); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &out)
}

// archiveAccount POST /api/accounts/{id}/archive
func (s *Server) archiveAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cur, err := s.st.GetAccount(id)
	if mapStoreError(w, err) {
		return
	}
	var req struct {
		Archived *bool `json:"archived"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	archived := true
	if req.Archived != nil {
		archived = *req.Archived
	}
	if err := s.st.SetArchived(id, archived); err != nil {
		mapStoreError(w, err)
		return
	}
	cur.Archived = archived
	if archived {
		cur.ArchivedAt = model.Today()
	} else {
		cur.ArchivedAt = ""
	}
	writeJSON(w, http.StatusOK, cur)
}
