package api

import (
	"net/http"
	"strings"

	"fam/server/internal/model"
)

// queryDate 读取并校验日期参数，空值时返回默认日期。
func queryDate(raw, def string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, true
	}
	return raw, model.ValidDate(raw)
}

// queryCurrency 读取并校验币种参数。
func queryCurrency(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	return raw, model.IsCurrency(raw)
}

// summary GET /api/stats/summary
func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	date, ok := queryDate(q.Get("date"), model.Today())
	if !ok {
		badRequest(w, "日期格式无效")
		return
	}
	currency, ok := queryCurrency(q.Get("currency"))
	if !ok {
		badRequest(w, "币种不合法")
		return
	}
	res, err := s.st.Summary(date, currency)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// timeseries GET /api/stats/timeseries
func (s *Server) timeseries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, ok := queryDate(q.Get("from"), "")
	if !ok {
		badRequest(w, "起始日期格式无效")
		return
	}
	to, ok := queryDate(q.Get("to"), model.Today())
	if !ok {
		badRequest(w, "结束日期格式无效")
		return
	}
	currency, ok := queryCurrency(q.Get("currency"))
	if !ok {
		badRequest(w, "币种不合法")
		return
	}
	groupBy := q.Get("groupBy")
	if groupBy == "" {
		groupBy = "day"
	}
	if groupBy != "day" && groupBy != "month" {
		badRequest(w, "groupBy 必须为 day 或 month")
		return
	}
	pts, err := s.st.TimeSeries(from, to, currency, groupBy)
	if err != nil {
		if strings.Contains(err.Error(), "区间") {
			badRequest(w, err.Error())
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"currency": currency, "from": from, "to": to, "groupBy": groupBy, "points": pts})
}

// compare GET /api/stats/compare
func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, ok := queryDate(q.Get("from"), "")
	if !ok {
		badRequest(w, "起始日期格式无效")
		return
	}
	to, ok := queryDate(q.Get("to"), model.Today())
	if !ok {
		badRequest(w, "结束日期格式无效")
		return
	}
	currency, ok := queryCurrency(q.Get("currency"))
	if !ok {
		badRequest(w, "币种不合法")
		return
	}
	res, err := s.st.Compare(from, to, currency)
	if err != nil {
		if strings.Contains(err.Error(), "区间") || strings.Contains(err.Error(), "日期") {
			badRequest(w, err.Error())
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
