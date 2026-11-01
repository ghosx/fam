package store

import (
	"errors"
	"sort"
	"time"

	"fam/server/internal/model"
)

// errBadRange 日期区间非法。
var errBadRange = errors.New("日期区间无效：起始日期不能晚于结束日期")

// errInvalidDate 日期格式非法。
var errInvalidDate = errors.New("日期格式无效")

// acctRow 统计用的账户行。
type acctRow struct {
	id         int64
	name       string
	category   string
	currency   string
	archivedAt string
}

// minorPoint 某账户某次录入的分值。
type minorPoint struct {
	date  string
	minor int64
}

// statsData 统计所需的内存数据集。
type statsData struct {
	accounts []acctRow
	points   map[int64][]minorPoint
}

// loadStatsData 载入账户（含归档时间）与截至 to 的全部录入点。
func (s *Store) loadStatsData(to string) (*statsData, error) {
	rows, err := s.db.Query(`SELECT id, name, category, currency, COALESCE(archived_at,'') FROM accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := &statsData{points: map[int64][]minorPoint{}}
	for rows.Next() {
		var a acctRow
		if err := rows.Scan(&a.id, &a.name, &a.category, &a.currency, &a.archivedAt); err != nil {
			return nil, err
		}
		d.accounts = append(d.accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	q := `SELECT account_id, entry_date, amount_minor FROM balance_entries`
	var args []any
	if to != "" {
		q += ` WHERE entry_date <= ?`
		args = append(args, to)
	}
	q += ` ORDER BY account_id, entry_date ASC, id ASC`
	er, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer er.Close()
	for er.Next() {
		var id int64
		var p minorPoint
		if err := er.Scan(&id, &p.date, &p.minor); err != nil {
			return nil, err
		}
		d.points[id] = append(d.points[id], p)
	}
	return d, er.Err()
}

// activeAt 判断账户在给定日期是否计入统计。
// 未归档账户恒计入；已归档账户仅在其归档日之前计入，保证历史走势不断裂。
func (a acctRow) activeAt(date string) bool {
	if a.archivedAt == "" {
		return true
	}
	return date < a.archivedAt
}

// latestAsOf 返回账户截至 date 的最近一次录入分值（无记录为 0）。
func latestAsOf(list []minorPoint, date string) int64 {
	lo, hi := 0, len(list)
	for lo < hi {
		mid := (lo + hi) / 2
		if list[mid].date <= date {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return 0
	}
	return list[lo-1].minor
}

// Summary 计算指定日期的总资产与分布。
// currency 为空时按 CNY 汇总，并附带全部币种的总额。
func (s *Store) Summary(date, currency string) (model.StatsSummary, error) {
	if date == "" {
		date = model.Today()
	}
	if currency == "" {
		currency = "CNY"
	}
	d, err := s.loadStatsData(date)
	if err != nil {
		return model.StatsSummary{}, err
	}

	out := model.StatsSummary{Date: date, Currency: currency,
		Categories: []model.CategoryStat{}, Accounts: []model.AccountStat{}}

	byCat := map[string]int64{}
	accStats := make([]model.AccountStat, 0)
	var total int64

	for _, a := range d.accounts {
		if a.currency != currency || !a.activeAt(date) {
			continue
		}
		m := latestAsOf(d.points[a.id], date)
		total += m
		byCat[a.category] += m
		accStats = append(accStats, model.AccountStat{
			AccountID: a.id, Name: a.name, Category: a.category, Amount: FromMinor(m),
		})
	}
	// 全部币种的总额（不受 currency 过滤影响）
	all := map[string]int64{}
	for _, a := range d.accounts {
		if !a.activeAt(date) {
			continue
		}
		all[a.currency] += latestAsOf(d.points[a.id], date)
	}
	codes := make([]string, 0, len(all))
	for c := range all {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		out.ByCurrency = append(out.ByCurrency, model.CurrencyTotal{Currency: c, Total: FromMinor(all[c])})
	}

	out.Total = FromMinor(total)
	for _, c := range model.Categories {
		if v, ok := byCat[c]; ok && v != 0 {
			out.Categories = append(out.Categories, model.CategoryStat{
				Category: c, Label: model.CategoryLabels[c], Amount: FromMinor(v), Ratio: ratio(v, total),
			})
		}
	}
	sort.Slice(out.Categories, func(i, j int) bool { return out.Categories[i].Amount > out.Categories[j].Amount })
	for i := range accStats {
		accStats[i].Ratio = ratio(ToMinor(accStats[i].Amount), total)
	}
	sort.Slice(accStats, func(i, j int) bool { return accStats[i].Amount > accStats[j].Amount })
	out.Accounts = accStats
	return out, nil
}

func ratio(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}

// dateRange 生成 [from, to] 内的日期点；groupBy=month 时按月生成（取当月最后一天计算）。
func dateRange(from, to string, groupBy string) ([]string, error) {
	f, err := time.Parse(model.DateLayout, from)
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(model.DateLayout, to)
	if err != nil {
		return nil, err
	}
	if t.Before(f) {
		return nil, errBadRange
	}
	out := []string{}
	if groupBy == "month" {
		cur := time.Date(f.Year(), f.Month(), 1, 0, 0, 0, 0, time.Local)
		for !cur.After(t) {
			last := time.Date(cur.Year(), cur.Month()+1, 0, 0, 0, 0, 0, time.Local)
			point := last
			if point.After(t) {
				point = t
			}
			if !point.Before(f) {
				out = append(out, point.Format("2006-01")+"|"+point.Format(model.DateLayout))
			}
			cur = time.Date(cur.Year(), cur.Month()+1, 1, 0, 0, 0, 0, time.Local)
		}
		return out, nil
	}
	for c := f; !c.After(t); c = c.AddDate(0, 0, 1) {
		out = append(out, c.Format(model.DateLayout)+"|"+c.Format(model.DateLayout))
	}
	return out, nil
}

// TimeSeries 计算区间内的资产走势（carry-forward 口径）。
func (s *Store) TimeSeries(from, to, currency, groupBy string) ([]model.SeriesPoint, error) {
	if from == "" {
		from = earliestDate(s)
	}
	if to == "" {
		to = model.Today()
	}
	if from == "" {
		return []model.SeriesPoint{}, nil
	}
	if currency == "" {
		currency = "CNY"
	}
	pts, err := dateRange(from, to, groupBy)
	if err != nil {
		return nil, err
	}
	d, err := s.loadStatsData(to)
	if err != nil {
		return nil, err
	}
	accts := make([]acctRow, 0, len(d.accounts))
	for _, a := range d.accounts {
		if a.currency == currency {
			accts = append(accts, a)
		}
	}
	out := make([]model.SeriesPoint, 0, len(pts))
	for _, p := range pts {
		label, value := splitPoint(p)
		var total int64
		byCat := map[string]float64{}
		for _, a := range accts {
			if !a.activeAt(value) {
				continue
			}
			m := latestAsOf(d.points[a.id], value)
			total += m
			byCat[a.category] += FromMinor(m)
		}
		out = append(out, model.SeriesPoint{Date: label, Total: FromMinor(total), ByCategory: byCat})
	}
	return out, nil
}

// splitPoint 拆分 "标签|计算日期"。
func splitPoint(p string) (label, value string) {
	for i := 0; i < len(p); i++ {
		if p[i] == '|' {
			return p[:i], p[i+1:]
		}
	}
	return p, p
}

// earliestDate 返回库中最早的录入日期。
func earliestDate(s *Store) string {
	var d string
	err := s.db.QueryRow(`SELECT COALESCE(MIN(entry_date),'') FROM balance_entries`).Scan(&d)
	if err != nil {
		return ""
	}
	return d
}

// Compare 计算区间起止总额与变化。
func (s *Store) Compare(from, to, currency string) (model.CompareResult, error) {
	if currency == "" {
		currency = "CNY"
	}
	if from == "" {
		from = earliestDate(s)
	}
	if to == "" {
		to = model.Today()
	}
	if from == "" {
		from = to
	}
	f, err := time.Parse(model.DateLayout, from)
	if err != nil {
		return model.CompareResult{}, errInvalidDate
	}
	t, err := time.Parse(model.DateLayout, to)
	if err != nil {
		return model.CompareResult{}, errInvalidDate
	}
	if t.Before(f) {
		return model.CompareResult{}, errBadRange
	}
	totalAt := func(date string) float64 {
		d, err := s.loadStatsData(date)
		if err != nil {
			return 0
		}
		var total int64
		for _, a := range d.accounts {
			if a.currency != currency || !a.activeAt(date) {
				continue
			}
			total += latestAsOf(d.points[a.id], date)
		}
		return FromMinor(total)
	}
	start, end := totalAt(from), totalAt(to)
	res := model.CompareResult{
		Currency: currency,
		Start:    model.Point{Date: from, Total: start},
		End:      model.Point{Date: to, Total: end},
		Change:   round2(end - start),
	}
	if start != 0 {
		res.ChangeRatio = (end - start) / start
	}
	return res, nil
}

// round2 保留两位小数，消除浮点尾差。
func round2(v float64) float64 {
	if v < 0 {
		return -float64(int64(-v*100+0.5)) / 100
	}
	return float64(int64(v*100+0.5)) / 100
}

// totalAtDate 指定日期与币种的总额（历史统计与导出复用）。
func (s *Store) totalAtDate(date, currency string) int64 {
	d, err := s.loadStatsData(date)
	if err != nil {
		return 0
	}
	var total int64
	for _, a := range d.accounts {
		if a.currency != currency || !a.activeAt(date) {
			continue
		}
		total += latestAsOf(d.points[a.id], date)
	}
	return total
}
