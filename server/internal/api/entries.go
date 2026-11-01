package api

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"fam/server/internal/model"
	"fam/server/internal/store"
)

// entriesRequest 录入请求体：date + items（1 条为单条录入，多条为批量录入）。
type entriesRequest struct {
	Date  string             `json:"date"`
	Items []model.EntryInput `json:"items"`
}

// validateDate 校验录入日期。
func validateDate(d string) (string, string) {
	d = strings.TrimSpace(d)
	if !model.ValidDate(d) {
		return "", "日期格式无效，应为 YYYY-MM-DD"
	}
	if d > model.Today() {
		return "", "录入日期不能晚于今天"
	}
	return d, ""
}

// validateAmount 校验金额并返回以「分」为单位的整数。
func validateAmount(v float64) (int64, string) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, "金额不是有效数字"
	}
	if math.Abs(v) > 1e12 {
		return 0, "金额超出允许范围（最大 1 万亿）"
	}
	minor := store.ToMinor(v)
	if math.Abs(store.FromMinor(minor)-v) > 1e-9 {
		return 0, "金额最多保留两位小数"
	}
	return minor, ""
}

// listEntries GET /api/entries
func (s *Server) listEntries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.EntryFilter{
		From:      strings.TrimSpace(q.Get("from")),
		To:        strings.TrimSpace(q.Get("to")),
		Page:      atoiDefault(q.Get("page"), 1),
		PageSize:  atoiDefault(q.Get("pageSize"), 50),
		AccountID: int64(atoiDefault(q.Get("accountId"), 0)),
	}
	if f.From != "" && !model.ValidDate(f.From) {
		badRequest(w, "起始日期格式无效")
		return
	}
	if f.To != "" && !model.ValidDate(f.To) {
		badRequest(w, "结束日期格式无效")
		return
	}
	page, err := s.st.ListEntries(f)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// createEntries POST /api/entries
func (s *Server) createEntries(w http.ResponseWriter, r *http.Request) {
	var req entriesRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	date, msg := validateDate(req.Date)
	if msg != "" {
		badRequest(w, msg)
		return
	}
	if len(req.Items) == 0 {
		badRequest(w, "录入明细不能为空")
		return
	}
	for _, it := range req.Items {
		if it.AccountID <= 0 {
			badRequest(w, "账户 id 无效")
			return
		}
		if !s.accountExists(it.AccountID) {
			badRequest(w, "账户不存在：id="+strconv.FormatInt(it.AccountID, 10))
			return
		}
		if it.Amount == nil {
			badRequest(w, "金额不能为空")
			return
		}
		if _, msg := validateAmount(*it.Amount); msg != "" {
			badRequest(w, msg)
			return
		}
		if _, msg := validateNote(it.Note); msg != "" {
			badRequest(w, msg)
			return
		}
	}
	n, err := s.st.UpsertEntries(date, req.Items)
	if mapStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"date": date, "saved": n})
}

// updateEntry PUT /api/entries/{id}
func (s *Server) updateEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cur, err := s.st.GetEntry(id)
	if mapStoreError(w, err) {
		return
	}
	var req struct {
		AccountID *int64   `json:"accountId"`
		Date      *string  `json:"date"`
		Amount    *float64 `json:"amount"`
		Note      *string  `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	accountID, date, minor, note := cur.AccountID, cur.Date, store.ToMinor(cur.Amount), cur.Note
	if req.AccountID != nil {
		if *req.AccountID <= 0 {
			badRequest(w, "账户 id 无效")
			return
		}
		if !s.accountExists(*req.AccountID) {
			badRequest(w, "账户不存在")
			return
		}
		accountID = *req.AccountID
	}
	if req.Date != nil {
		d, msg := validateDate(*req.Date)
		if msg != "" {
			badRequest(w, msg)
			return
		}
		date = d
	}
	if req.Amount != nil {
		m, msg := validateAmount(*req.Amount)
		if msg != "" {
			badRequest(w, msg)
			return
		}
		minor = m
	}
	if req.Note != nil {
		n, msg := validateNote(*req.Note)
		if msg != "" {
			badRequest(w, msg)
			return
		}
		note = n
	}
	if err := s.st.UpdateEntry(id, accountID, date, minor, note); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "accountId": accountID, "date": date, "amount": store.FromMinor(minor), "note": note})
}

// deleteEntry DELETE /api/entries/{id}
func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteEntry(id); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// accountExists 判断账户是否存在。
func (s *Server) accountExists(id int64) bool {
	_, err := s.st.GetAccount(id)
	return err == nil
}

// atoiDefault 解析整数参数，失败或缺失时返回默认值。
func atoiDefault(raw string, def int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}
