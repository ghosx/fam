package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"

	"fam/server/internal/model"
	"fam/server/internal/store"
)

// csvBOM UTF-8 BOM，保证 Excel 打开中文不乱码。
var csvBOM = []byte{0xEF, 0xBB, 0xBF}

// resolveFormat 解析导出格式，仅支持 csv 与 json。
func resolveFormat(w http.ResponseWriter, raw string) (string, bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "csv", true
	}
	if raw != "csv" && raw != "json" {
		badRequest(w, "导出格式必须为 csv 或 json")
		return "", false
	}
	return raw, true
}

// writeCSV 输出带 BOM 的 CSV 附件。
func writeCSV(w http.ResponseWriter, filename string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(csvBOM)
	cw := csv.NewWriter(w)
	_ = cw.WriteAll(rows)
	cw.Flush()
}

// writeJSONArray 以数组形式输出 JSON 导出结果。
func writeJSONArray(w http.ResponseWriter, filename string, v any) {
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	writeJSON(w, http.StatusOK, v)
}

// dateRangeParams 解析导出用的日期区间。
func dateRangeParams(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	from, ok := queryDate(r.URL.Query().Get("from"), "")
	if !ok {
		badRequest(w, "起始日期格式无效")
		return "", "", false
	}
	to, ok := queryDate(r.URL.Query().Get("to"), model.Today())
	if !ok {
		badRequest(w, "结束日期格式无效")
		return "", "", false
	}
	return from, to, true
}

// exportEntries GET /api/export/entries
func (s *Server) exportEntries(w http.ResponseWriter, r *http.Request) {
	format, ok := resolveFormat(w, r.URL.Query().Get("format"))
	if !ok {
		return
	}
	from, to, ok := dateRangeParams(w, r)
	if !ok {
		return
	}
	list, err := s.st.AllEntriesForExport(from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	if format == "json" {
		writeJSONArray(w, "fam-entries.json", list)
		return
	}
	rows := [][]string{{"日期", "账户", "类别", "币种", "金额", "备注"}}
	for _, e := range list {
		rows = append(rows, []string{e.Date, e.AccountName, model.CategoryLabels[e.Category], e.Currency,
			store.FormatAmount(store.ToMinor(e.Amount)), e.Note})
	}
	writeCSV(w, "fam-entries.csv", rows)
}

// exportSummary GET /api/export/summary
func (s *Server) exportSummary(w http.ResponseWriter, r *http.Request) {
	format, ok := resolveFormat(w, r.URL.Query().Get("format"))
	if !ok {
		return
	}
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
	if format == "json" {
		writeJSONArray(w, "fam-summary.json", res)
		return
	}
	rows := [][]string{{"类型", "名称", "金额", "占比"}}
	for _, c := range res.Categories {
		rows = append(rows, []string{"类别", c.Label, store.FormatAmount(store.ToMinor(c.Amount)), percent(c.Ratio)})
	}
	for _, a := range res.Accounts {
		rows = append(rows, []string{"账户", a.Name, store.FormatAmount(store.ToMinor(a.Amount)), percent(a.Ratio)})
	}
	rows = append(rows, []string{"总计", res.Currency, store.FormatAmount(store.ToMinor(res.Total)), "100.00%"})
	writeCSV(w, "fam-summary.csv", rows)
}

// exportTimeseries GET /api/export/timeseries
func (s *Server) exportTimeseries(w http.ResponseWriter, r *http.Request) {
	format, ok := resolveFormat(w, r.URL.Query().Get("format"))
	if !ok {
		return
	}
	from, to, ok := dateRangeParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
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
		badRequest(w, err.Error())
		return
	}
	if format == "json" {
		writeJSONArray(w, "fam-timeseries.json", pts)
		return
	}
	rows := [][]string{{"日期", "总资产"}}
	for _, p := range pts {
		rows = append(rows, []string{p.Date, store.FormatAmount(store.ToMinor(p.Total))})
	}
	writeCSV(w, "fam-timeseries.csv", rows)
}

// percent 把比例格式化为百分比字符串。
func percent(v float64) string {
	return fmt.Sprintf("%.2f%%", v*100)
}
