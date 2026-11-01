// Package model 定义 fam 的领域数据结构与枚举校验。
package model

import "time"

// 账户类别枚举。
var Categories = []string{
	"bank", "alipay", "wechat", "stock", "securities", "insurance", "cash", "other",
}

// 类别中文名。
var CategoryLabels = map[string]string{
	"bank":       "银行",
	"alipay":     "支付宝",
	"wechat":     "微信",
	"stock":      "股票",
	"securities": "证券",
	"insurance":  "保险",
	"cash":       "现金",
	"other":      "其他",
}

// 支持的币种。
var Currencies = []string{"CNY", "USD", "HKD", "EUR", "JPY"}

// 日期格式：YYYY-MM-DD。
const DateLayout = "2006-01-02"

// 快照格式版本，用于备份恢复的兼容性校验。
const SnapshotVersion = 1

// Account 财产账户。
type Account struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Currency   string `json:"currency"`
	Note       string `json:"note"`
	Archived   bool   `json:"archived"`
	ArchivedAt string `json:"archivedAt,omitempty"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	// 以下为列表接口的派生字段
	LatestAmount *float64 `json:"latestAmount,omitempty"`
	LatestDate   string   `json:"latestDate,omitempty"`
}

// Entry 一条人工录入记录。
type Entry struct {
	ID          int64   `json:"id"`
	AccountID   int64   `json:"accountId"`
	AccountName string  `json:"accountName,omitempty"`
	Category    string  `json:"category,omitempty"`
	Currency    string  `json:"currency,omitempty"`
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"`
	Note        string  `json:"note"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

// EntryInput 录入请求中的单条明细。
type EntryInput struct {
	AccountID int64    `json:"accountId"`
	Amount    *float64 `json:"amount"`
	Note      string   `json:"note"`
}

// StatsSummary 某日的总资产与分布。
type StatsSummary struct {
	Date       string          `json:"date"`
	Currency   string          `json:"currency"`
	Total      float64         `json:"total"`
	Categories []CategoryStat  `json:"categories"`
	Accounts   []AccountStat   `json:"accounts"`
	ByCurrency []CurrencyTotal `json:"byCurrency,omitempty"`
}

// CategoryStat 类别分布。
type CategoryStat struct {
	Category string  `json:"category"`
	Label    string  `json:"label"`
	Amount   float64 `json:"amount"`
	Ratio    float64 `json:"ratio"`
}

// AccountStat 账户分布。
type AccountStat struct {
	AccountID int64   `json:"accountId"`
	Name      string  `json:"name"`
	Category  string  `json:"category"`
	Amount    float64 `json:"amount"`
	Ratio     float64 `json:"ratio"`
}

// CurrencyTotal 单一币种的总额。
type CurrencyTotal struct {
	Currency string  `json:"currency"`
	Total    float64 `json:"total"`
}

// SeriesPoint 时间序列上的一个点。
type SeriesPoint struct {
	Date       string             `json:"date"`
	Total      float64            `json:"total"`
	ByCategory map[string]float64 `json:"byCategory,omitempty"`
}

// CompareResult 区间对比结果。
type CompareResult struct {
	Currency    string  `json:"currency"`
	Start       Point   `json:"start"`
	End         Point   `json:"end"`
	Change      float64 `json:"change"`
	ChangeRatio float64 `json:"changeRatio"`
}

// Point 区间端点。
type Point struct {
	Date  string  `json:"date"`
	Total float64 `json:"total"`
}

// Snapshot 备份快照。
type Snapshot struct {
	Version    int             `json:"version"`
	ExportedAt string          `json:"exportedAt"`
	Accounts   []SnapshotAcc   `json:"accounts"`
	Entries    []SnapshotEntry `json:"entries"`
}

// SnapshotAcc 快照中的账户。
type SnapshotAcc struct {
	Name       string `json:"name"`
	Category   string `json:"category"`
	Currency   string `json:"currency"`
	Note       string `json:"note"`
	Archived   bool   `json:"archived"`
	ArchivedAt string `json:"archivedAt,omitempty"`
}

// SnapshotEntry 快照中的录入记录（以账户名+币种定位账户）。
type SnapshotEntry struct {
	AccountName string  `json:"accountName"`
	Currency    string  `json:"currency"`
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"`
	Note        string  `json:"note"`
}

// IsCategory 判断类别是否合法。
func IsCategory(c string) bool {
	for _, v := range Categories {
		if v == c {
			return true
		}
	}
	return false
}

// IsCurrency 判断币种是否合法。
func IsCurrency(c string) bool {
	for _, v := range Currencies {
		if v == c {
			return true
		}
	}
	return false
}

// ValidDate 判断是否为合法日期字符串。
func ValidDate(s string) bool {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return false
	}
	return t.Format(DateLayout) == s
}

// Today 返回本机当前日期字符串。
func Today() string {
	return time.Now().Format(DateLayout)
}
