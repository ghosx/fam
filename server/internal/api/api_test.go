package api

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"fam/server/internal/model"
	"fam/server/internal/store"
)

// env 测试环境：临时数据库 + 路由。
type env struct {
	t      *testing.T
	st     *store.Store
	h      http.Handler
	last   string
	token  string
	remote string
}

// 测试用密码。
const testPassword = "fam123456"

// newEnvBare 创建一个尚未设置密码的测试环境。
func newEnvBare(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "fam.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &env{t: t, st: st, h: New(st, "")}
}

// newEnv 创建一个已完成密码设置并登录的测试环境。
func newEnv(t *testing.T) *env {
	t.Helper()
	e := newEnvBare(t)
	e.token = e.setupPassword(testPassword)
	return e
}

// setupPassword 设置访问密码并返回会话令牌。
func (e *env) setupPassword(pw string) string {
	e.t.Helper()
	rec := e.code(e.anonDo("POST", "/api/auth/setup", map[string]any{"password": pw}), http.StatusCreated)
	res := decode[map[string]any](e, rec)
	token, _ := res["token"].(string)
	if token == "" {
		e.t.Fatalf("设置密码后未返回令牌: %v", res)
	}
	return token
}

// request 发起一次请求，withAuth 控制是否携带令牌。
func (e *env) request(method, path string, body any, withAuth bool) *httptest.ResponseRecorder {
	e.t.Helper()
	e.last = method + " " + path
	var rdr io.Reader
	if body != nil {
		switch v := body.(type) {
		case string:
			rdr = strings.NewReader(v)
		default:
			b, err := json.Marshal(v)
			if err != nil {
				e.t.Fatalf("序列化请求体失败: %v", err)
			}
			rdr = bytes.NewReader(b)
		}
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if e.remote != "" {
		req.RemoteAddr = e.remote
	}
	if withAuth && e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// do 发起一次带令牌的请求。
func (e *env) do(method, path string, body any) *httptest.ResponseRecorder {
	return e.request(method, path, body, true)
}

// anonDo 发起一次不带令牌的请求。
func (e *env) anonDo(method, path string, body any) *httptest.ResponseRecorder {
	return e.request(method, path, body, false)
}

// code 断言状态码。
func (e *env) code(rec *httptest.ResponseRecorder, want int) *httptest.ResponseRecorder {
	e.t.Helper()
	if rec.Code != want {
		e.t.Fatalf("%s: 期望状态码 %d，实际 %d，响应=%s", e.last, want, rec.Code, rec.Body.String())
	}
	return rec
}

// decode 解析响应体。
func decode[T any](e *env, rec *httptest.ResponseRecorder) T {
	e.t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		e.t.Fatalf("解析响应失败(%s): %v | body=%s", e.last, err, rec.Body.String())
	}
	return v
}

// accountID 创建账户并返回 id。
func (e *env) accountID(name, category, currency string) int64 {
	e.t.Helper()
	rec := e.code(e.do("POST", "/api/accounts", map[string]any{
		"name": name, "category": category, "currency": currency,
	}), http.StatusCreated)
	return decode[model.Account](e, rec).ID
}

// addEntry 录入一条记录。
func (e *env) addEntry(date string, accountID int64, amount float64) {
	e.t.Helper()
	e.code(e.do("POST", "/api/entries", map[string]any{
		"date": date,
		"items": []map[string]any{
			{"accountId": accountID, "amount": amount},
		},
	}), http.StatusCreated)
}

// accountsOf 查询账户列表。
func (e *env) accountsOf(query string) []model.Account {
	e.t.Helper()
	rec := e.code(e.do("GET", "/api/accounts"+query, nil), http.StatusOK)
	type resp struct {
		Items []model.Account `json:"items"`
	}
	return decode[resp](e, rec).Items
}

// entriesOf 查询历史录入。
func (e *env) entriesOf(query string) store.EntryPage {
	e.t.Helper()
	rec := e.code(e.do("GET", "/api/entries"+query, nil), http.StatusOK)
	return decode[store.EntryPage](e, rec)
}

// ---------- A. 账户管理 ----------

func TestA01_CreateAccount(t *testing.T) {
	e := newEnv(t)
	rec := e.code(e.do("POST", "/api/accounts", map[string]any{
		"name": "招商银行储蓄卡", "category": "bank", "currency": "CNY", "note": "工资卡",
	}), http.StatusCreated)
	a := decode[model.Account](e, rec)
	if a.ID == 0 || a.Name != "招商银行储蓄卡" || a.Category != "bank" || a.Currency != "CNY" || a.Note != "工资卡" {
		t.Fatalf("账户字段回显不一致: %+v", a)
	}
	if a.Archived || a.CreatedAt == "" {
		t.Fatalf("新账户状态不正确: %+v", a)
	}
}

func TestA02_CreateWithoutName(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/accounts", map[string]any{"category": "bank", "currency": "CNY"}), http.StatusBadRequest)
	e.code(e.do("POST", "/api/accounts", map[string]any{"name": "  ", "category": "bank", "currency": "CNY"}), http.StatusBadRequest)
}

func TestA03_CreateBadCategory(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/accounts", map[string]any{"name": "A", "category": "crypto", "currency": "CNY"}), http.StatusBadRequest)
}

func TestA04_CreateBadCurrency(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/accounts", map[string]any{"name": "A", "category": "bank", "currency": "BTC"}), http.StatusBadRequest)
}

func TestA05_DuplicateNameConflict(t *testing.T) {
	e := newEnv(t)
	e.accountID("工商银行", "bank", "CNY")
	e.code(e.do("POST", "/api/accounts", map[string]any{"name": "工商银行", "category": "bank", "currency": "CNY"}), http.StatusConflict)
}

func TestA06_SameNameDifferentCurrency(t *testing.T) {
	e := newEnv(t)
	e.accountID("现金", "cash", "CNY")
	e.code(e.do("POST", "/api/accounts", map[string]any{"name": "现金", "category": "cash", "currency": "USD"}), http.StatusCreated)
}

func TestA07_NameTooLong(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/accounts", map[string]any{"name": strings.Repeat("x", 51), "category": "bank", "currency": "CNY"}), http.StatusBadRequest)
	e.code(e.do("POST", "/api/accounts", map[string]any{"name": strings.Repeat("x", 50), "category": "bank", "currency": "CNY"}), http.StatusCreated)
}

func TestA08_NoteTooLong(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/accounts", map[string]any{"name": "A", "category": "bank", "currency": "CNY", "note": strings.Repeat("x", 201)}), http.StatusBadRequest)
}

func TestA09_UpdateAccount(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("支付宝", "alipay", "CNY")
	rec := e.code(e.do("PUT", "/api/accounts/1", map[string]any{
		"name": "支付宝主账户", "category": "wechat", "currency": "USD", "note": "已改",
	}), http.StatusOK)
	a := decode[model.Account](e, rec)
	if a.Name != "支付宝主账户" || a.Category != "wechat" || a.Currency != "USD" || a.Note != "已改" {
		t.Fatalf("更新后字段不一致: %+v", a)
	}
	got := e.accountsOf("")[0]
	if got.Name != "支付宝主账户" || got.UpdatedAt != a.UpdatedAt {
		t.Fatalf("再次查询结果不一致: %+v", got)
	}
	_ = id
}

func TestA10_UpdateToDuplicate(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("甲", "bank", "CNY")
	b := e.accountID("乙", "bank", "CNY")
	_ = a
	e.code(e.do("PUT", "/api/accounts/"+itoa(b), map[string]any{"name": "甲"}), http.StatusConflict)
}

func TestA11_ArchiveAccount(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("已销户卡", "bank", "CNY")
	rec := e.code(e.do("POST", "/api/accounts/"+itoa(id)+"/archive", map[string]any{"archived": true}), http.StatusOK)
	a := decode[model.Account](e, rec)
	if !a.Archived || a.ArchivedAt != model.Today() {
		t.Fatalf("归档状态不正确: %+v", a)
	}
}

func TestA12_ArchivedHiddenByDefault(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("销户", "bank", "CNY")
	e.code(e.do("POST", "/api/accounts/"+itoa(id)+"/archive", map[string]any{"archived": true}), http.StatusOK)
	if len(e.accountsOf("")) != 0 {
		t.Fatalf("默认列表不应包含已归档账户")
	}
}

func TestA13_IncludeArchived(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("销户", "bank", "CNY")
	e.code(e.do("POST", "/api/accounts/"+itoa(id)+"/archive", map[string]any{"archived": true}), http.StatusOK)
	if len(e.accountsOf("?includeArchived=true")) != 1 {
		t.Fatalf("includeArchived=true 应返回已归档账户")
	}
}

func TestA14_Unarchive(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("销户", "bank", "CNY")
	e.code(e.do("POST", "/api/accounts/"+itoa(id)+"/archive", map[string]any{"archived": true}), http.StatusOK)
	rec := e.code(e.do("POST", "/api/accounts/"+itoa(id)+"/archive", map[string]any{"archived": false}), http.StatusOK)
	a := decode[model.Account](e, rec)
	if a.Archived || a.ArchivedAt != "" {
		t.Fatalf("取消归档后状态不正确: %+v", a)
	}
	if len(e.accountsOf("")) != 1 {
		t.Fatalf("取消归档后应重新出现在默认列表")
	}
}

func TestA15_LatestAmountInList(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("微信", "wechat", "CNY")
	e.addEntry("2024-03-01", id, 100)
	e.addEntry("2024-05-01", id, 250.5)
	list := e.accountsOf("")
	if len(list) != 1 || list[0].LatestAmount == nil || math.Abs(*list[0].LatestAmount-250.5) > 1e-9 {
		t.Fatalf("最新余额不正确: %+v", list)
	}
	if list[0].LatestDate != "2024-05-01" {
		t.Fatalf("最新日期不正确: %s", list[0].LatestDate)
	}
}

func TestA16_AccountNotFound(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("GET", "/api/accounts/999", nil), http.StatusNotFound)
	e.code(e.do("PUT", "/api/accounts/999", map[string]any{"name": "x"}), http.StatusNotFound)
}

func TestA17_KeywordFilter(t *testing.T) {
	e := newEnv(t)
	e.accountID("招商银行", "bank", "CNY")
	e.accountID("微信零钱", "wechat", "CNY")
	if len(e.accountsOf("?keyword=招商")) != 1 {
		t.Fatalf("关键字筛选结果不正确")
	}
}

// ---------- B. 录入 ----------

func TestB01_SingleEntry(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", id, 1000)
	page := e.entriesOf("")
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Amount != 1000 {
		t.Fatalf("单条录入结果不正确: %+v", page)
	}
}

func TestB02_BatchEntry(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行A", "bank", "CNY")
	b := e.accountID("银行B", "bank", "CNY")
	rec := e.code(e.do("POST", "/api/entries", map[string]any{
		"date": "2024-02-01",
		"items": []map[string]any{
			{"accountId": a, "amount": 100},
			{"accountId": b, "amount": 200.25},
		},
	}), http.StatusCreated)
	res := decode[map[string]any](e, rec)
	if res["saved"].(float64) != 2 {
		t.Fatalf("批量保存条数不正确: %v", res)
	}
	if e.entriesOf("").Total != 2 {
		t.Fatalf("批量录入未全部入库")
	}
}

func TestB03_UpsertSameDate(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", id, 100)
	e.addEntry("2024-01-01", id, 300)
	page := e.entriesOf("?accountId=" + itoa(id))
	if page.Total != 1 {
		t.Fatalf("同日重复录入应覆盖，实际 %d 条", page.Total)
	}
	if page.Items[0].Amount != 300 {
		t.Fatalf("覆盖后金额不正确: %v", page.Items[0].Amount)
	}
}

func TestB04_NegativeAmount(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("信用卡", "bank", "CNY")
	e.addEntry("2024-01-01", id, -500.75)
	page := e.entriesOf("")
	if page.Items[0].Amount != -500.75 {
		t.Fatalf("负数金额存储不正确: %v", page.Items[0].Amount)
	}
}

func TestB05_AmountPrecision(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("银行", "bank", "CNY")
	// 超过两位小数的金额应被拒绝，避免静默舍入
	e.code(e.do("POST", "/api/entries", map[string]any{
		"date":  "2024-01-01",
		"items": []map[string]any{{"accountId": id, "amount": 1234.565}},
	}), http.StatusBadRequest)
	// 两位小数金额精确存储
	e.addEntry("2024-01-01", id, 1234.56)
	if got := e.entriesOf("").Items[0].Amount; math.Abs(got-1234.56) > 1e-9 {
		t.Fatalf("金额存储不精确: %v", got)
	}
	// 以「分」为单位累加，避免浮点误差
	c1 := e.accountID("零钱一", "cash", "CNY")
	c2 := e.accountID("零钱二", "cash", "CNY")
	e.addEntry("2024-01-01", c1, 0.1)
	e.addEntry("2024-01-01", c2, 0.2)
	if got := e.summary("2024-01-01", "CNY").Total; math.Abs(got-1234.86) > 1e-9 {
		t.Fatalf("汇总精度不正确: %v", got)
	}
}

func TestB06_BadDateFormat(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("银行", "bank", "CNY")
	e.code(e.do("POST", "/api/entries", map[string]any{
		"date":  "2024/01/01",
		"items": []map[string]any{{"accountId": id, "amount": 1}},
	}), http.StatusBadRequest)
}

func TestB07_FutureDate(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("银行", "bank", "CNY")
	e.code(e.do("POST", "/api/entries", map[string]any{
		"date":  "2099-01-01",
		"items": []map[string]any{{"accountId": id, "amount": 1}},
	}), http.StatusBadRequest)
}

func TestB08_UnknownAccount(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/entries", map[string]any{
		"date":  "2024-01-01",
		"items": []map[string]any{{"accountId": 9999, "amount": 1}},
	}), http.StatusBadRequest)
}

func TestB09_EmptyItems(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/entries", map[string]any{"date": "2024-01-01", "items": []map[string]any{}}), http.StatusBadRequest)
}

func TestB10_InvalidAmount(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("银行", "bank", "CNY")
	e.code(e.do("POST", "/api/entries", map[string]any{
		"date":  "2024-01-01",
		"items": []map[string]any{{"accountId": id}},
	}), http.StatusBadRequest)
	e.code(e.do("POST", "/api/entries", map[string]string{"date": "2024-01-01"}), http.StatusBadRequest)
}

func TestB11_AmountOverflow(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("银行", "bank", "CNY")
	e.code(e.do("POST", "/api/entries", map[string]any{
		"date":  "2024-01-01",
		"items": []map[string]any{{"accountId": id, "amount": 2e12}},
	}), http.StatusBadRequest)
}

func TestB12_EntryOrder(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", id, 1)
	e.addEntry("2024-03-01", id, 2)
	e.addEntry("2024-02-01", id, 3)
	items := e.entriesOf("").Items
	if items[0].Date != "2024-03-01" || items[1].Date != "2024-02-01" || items[2].Date != "2024-01-01" {
		t.Fatalf("历史记录未按日期倒序: %+v", items)
	}
}

func TestB13_FilterByAccount(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	b := e.accountID("B", "bank", "CNY")
	e.addEntry("2024-01-01", a, 1)
	e.addEntry("2024-01-01", b, 2)
	page := e.entriesOf("?accountId=" + itoa(a))
	if page.Total != 1 || page.Items[0].AccountID != a {
		t.Fatalf("按账户筛选不正确: %+v", page)
	}
}

func TestB14_FilterByRange(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("A", "bank", "CNY")
	e.addEntry("2023-12-31", id, 1)
	e.addEntry("2024-01-01", id, 2)
	e.addEntry("2024-01-05", id, 3)
	e.addEntry("2024-02-01", id, 4)
	page := e.entriesOf("?from=2024-01-01&to=2024-01-05")
	if page.Total != 2 {
		t.Fatalf("区间筛选应为闭区间 2 条，实际 %d", page.Total)
	}
}

func TestB15_UpdateEntryAmount(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("A", "bank", "CNY")
	e.addEntry("2024-01-01", id, 100)
	entryID := e.entriesOf("").Items[0].ID
	e.code(e.do("PUT", "/api/entries/"+itoa(entryID), map[string]any{"amount": 250}), http.StatusOK)
	if got := e.entriesOf("").Items[0].Amount; got != 250 {
		t.Fatalf("修改后金额不正确: %v", got)
	}
	sum := e.summary("2024-01-02", "CNY")
	if sum.Total != 250 {
		t.Fatalf("汇总未随修改更新: %v", sum.Total)
	}
}

func TestB16_UpdateEntryDateConflict(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("A", "bank", "CNY")
	e.addEntry("2024-01-01", id, 100)
	e.addEntry("2024-01-02", id, 200)
	first := e.entriesOf("?from=2024-01-01&to=2024-01-01").Items[0].ID
	e.code(e.do("PUT", "/api/entries/"+itoa(first), map[string]any{"date": "2024-01-02"}), http.StatusConflict)
}

func TestB17_DeleteEntry(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("A", "bank", "CNY")
	e.addEntry("2024-01-01", id, 100)
	entryID := e.entriesOf("").Items[0].ID
	e.code(e.do("DELETE", "/api/entries/"+itoa(entryID), nil), http.StatusOK)
	if e.entriesOf("").Total != 0 {
		t.Fatalf("删除后仍有记录")
	}
}

func TestB18_DeleteMissingEntry(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("DELETE", "/api/entries/12345", nil), http.StatusNotFound)
}

func TestB19_Pagination(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("A", "bank", "CNY")
	for i := 1; i <= 5; i++ {
		e.addEntry("2024-01-0"+strconv.Itoa(i), id, float64(i))
	}
	page := e.entriesOf("?page=2&pageSize=2")
	if page.Total != 5 || page.Page != 2 || len(page.Items) != 2 {
		t.Fatalf("分页结果不正确: %+v", page)
	}
}

func TestB20_BatchRollback(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	e.code(e.do("POST", "/api/entries", map[string]any{
		"date": "2024-01-01",
		"items": []map[string]any{
			{"accountId": a, "amount": 100},
			{"accountId": 9999, "amount": 100},
		},
	}), http.StatusBadRequest)
	if e.entriesOf("").Total != 0 {
		t.Fatalf("部分失败时应整体回滚，实际 %d 条", e.entriesOf("").Total)
	}
}

// ---------- C. 统计与走势 ----------

// summary 查询指定日期与币种的汇总。
func (e *env) summary(date, currency string) model.StatsSummary {
	e.t.Helper()
	q := "?date=" + date + "&currency=" + currency
	rec := e.code(e.do("GET", "/api/stats/summary"+q, nil), http.StatusOK)
	return decode[model.StatsSummary](e, rec)
}

// series 查询时间序列。
func (e *env) series(query string) []model.SeriesPoint {
	e.t.Helper()
	rec := e.code(e.do("GET", "/api/stats/timeseries"+query, nil), http.StatusOK)
	return decode[struct {
		Points []model.SeriesPoint `json:"points"`
	}](e, rec).Points
}

func TestC01_TotalSingleCurrency(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	b := e.accountID("B", "alipay", "CNY")
	e.addEntry("2024-01-01", a, 1000.10)
	e.addEntry("2024-01-01", b, 2000.20)
	sum := e.summary("2024-01-01", "CNY")
	if math.Abs(sum.Total-3000.30) > 1e-9 {
		t.Fatalf("总资产不正确: %v", sum.Total)
	}
}

func TestC02_MultiCurrencyTotals(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	b := e.accountID("B", "bank", "USD")
	e.addEntry("2024-01-01", a, 100)
	e.addEntry("2024-01-01", b, 50)
	rec := e.code(e.do("GET", "/api/stats/summary?date=2024-01-01", nil), http.StatusOK)
	sum := decode[model.StatsSummary](e, rec)
	got := map[string]float64{}
	for _, c := range sum.ByCurrency {
		got[c.Currency] = c.Total
	}
	if math.Abs(got["CNY"]-100) > 1e-9 || math.Abs(got["USD"]-50) > 1e-9 {
		t.Fatalf("多币种总额不正确: %+v", got)
	}
	if math.Abs(sum.Total-100) > 1e-9 || sum.Currency != "CNY" {
		t.Fatalf("默认币种应为 CNY: %+v", sum)
	}
}

func TestC03_CategoryRatioSum(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	b := e.accountID("B", "alipay", "CNY")
	c := e.accountID("C", "stock", "CNY")
	e.addEntry("2024-01-01", a, 300)
	e.addEntry("2024-01-01", b, 200)
	e.addEntry("2024-01-01", c, -100)
	sum := e.summary("2024-01-01", "CNY")
	var total float64
	for _, c := range sum.Categories {
		total += c.Ratio
	}
	if math.Abs(total-1) > 1e-9 {
		t.Fatalf("类别占比之和应为 1，实际 %v", total)
	}
}

func TestC04_AccountRatioSum(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	b := e.accountID("B", "bank", "CNY")
	e.addEntry("2024-01-01", a, 750)
	e.addEntry("2024-01-01", b, 250)
	sum := e.summary("2024-01-01", "CNY")
	var total float64
	for _, c := range sum.Accounts {
		total += c.Ratio
	}
	if math.Abs(total-1) > 1e-9 {
		t.Fatalf("账户占比之和应为 1，实际 %v", total)
	}
	if len(sum.Accounts) != 2 || sum.Accounts[0].Name != "A" {
		t.Fatalf("账户分布排序不正确: %+v", sum.Accounts)
	}
}

func TestC05_CarryForward(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	e.addEntry("2024-01-05", a, 200)
	pts := e.series("?from=2024-01-01&to=2024-01-07&currency=CNY")
	if len(pts) != 7 {
		t.Fatalf("应返回 7 个点，实际 %d", len(pts))
	}
	want := map[string]float64{
		"2024-01-01": 100, "2024-01-02": 100, "2024-01-03": 100, "2024-01-04": 100,
		"2024-01-05": 200, "2024-01-06": 200, "2024-01-07": 200,
	}
	for _, p := range pts {
		if math.Abs(p.Total-want[p.Date]) > 1e-9 {
			t.Fatalf("%s 的资产应为 %v，实际 %v", p.Date, want[p.Date], p.Total)
		}
	}
}

func TestC06_MultiAccountSeries(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	b := e.accountID("B", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	e.addEntry("2024-01-01", b, 50)
	e.addEntry("2024-01-03", b, 80)
	pts := e.series("?from=2024-01-01&to=2024-01-03&currency=CNY")
	want := []float64{150, 150, 180}
	for i, p := range pts {
		if math.Abs(p.Total-want[i]) > 1e-9 {
			t.Fatalf("%s 合计应为 %v，实际 %v", p.Date, want[i], p.Total)
		}
	}
}

func TestC07_Compare(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	e.addEntry("2024-01-01", a, 1000)
	e.addEntry("2024-02-01", a, 1500)
	rec := e.code(e.do("GET", "/api/stats/compare?from=2024-01-01&to=2024-02-01&currency=CNY", nil), http.StatusOK)
	res := decode[model.CompareResult](e, rec)
	if res.Start.Total != 1000 || res.End.Total != 1500 || res.Change != 500 || math.Abs(res.ChangeRatio-0.5) > 1e-9 {
		t.Fatalf("区间对比结果不正确: %+v", res)
	}
}

func TestC08_StartWithoutData(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	e.addEntry("2024-01-05", a, 100)
	pts := e.series("?from=2024-01-01&to=2024-01-06&currency=CNY")
	if len(pts) != 6 || pts[0].Total != 0 || pts[5].Total != 100 {
		t.Fatalf("起始日无数据应为 0: %+v", pts)
	}
}

func TestC09_EmptyDatabase(t *testing.T) {
	e := newEnv(t)
	sum := e.summary("2024-01-01", "CNY")
	if sum.Total != 0 || len(sum.Categories) != 0 || len(sum.Accounts) != 0 {
		t.Fatalf("空库统计应为空: %+v", sum)
	}
	rec := e.code(e.do("GET", "/api/stats/compare?from=2024-01-01&to=2024-01-02&currency=CNY", nil), http.StatusOK)
	res := decode[model.CompareResult](e, rec)
	if res.Start.Total != 0 || res.End.Total != 0 || res.Change != 0 || res.ChangeRatio != 0 {
		t.Fatalf("空库对比应为 0: %+v", res)
	}
}

func TestC10_ArchivedInHistory(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	b := e.accountID("B", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	e.addEntry("2024-01-01", b, 100)
	e.code(e.do("POST", "/api/accounts/"+itoa(b)+"/archive", map[string]any{"archived": true}), http.StatusOK)
	if got := e.summary("2024-01-02", "CNY").Total; got != 200 {
		t.Fatalf("归档日之前的历史统计应仍包含该账户: %v", got)
	}
	if got := e.summary(model.Today(), "CNY").Total; got != 100 {
		t.Fatalf("当前总资产应排除已归档账户: %v", got)
	}
}

func TestC11_MonthlyGroupBy(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	e.addEntry("2024-01-15", a, 100)
	e.addEntry("2024-02-10", a, 200)
	e.addEntry("2024-03-01", a, 300)
	pts := e.series("?from=2024-01-01&to=2024-03-31&currency=CNY&groupBy=month")
	if len(pts) != 3 {
		t.Fatalf("月度聚合应为 3 个点，实际 %d", len(pts))
	}
	if pts[0].Date != "2024-01" || pts[0].Total != 100 || pts[1].Total != 200 || pts[2].Total != 300 {
		t.Fatalf("月度聚合结果不正确: %+v", pts)
	}
}

func TestC12_HistoricalDateSummary(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	e.addEntry("2024-06-01", a, 900)
	if got := e.summary("2024-03-01", "CNY").Total; got != 100 {
		t.Fatalf("历史日期汇总不正确: %v", got)
	}
}

func TestC13_CurrencyFilter(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("A", "bank", "CNY")
	b := e.accountID("B", "bank", "USD")
	e.addEntry("2024-01-01", a, 100)
	e.addEntry("2024-01-01", b, 999)
	if got := e.summary("2024-01-01", "CNY").Total; got != 100 {
		t.Fatalf("币种过滤不正确: %v", got)
	}
	if got := e.summary("2024-01-01", "USD").Total; got != 999 {
		t.Fatalf("币种过滤不正确: %v", got)
	}
}

func TestC14_NegativeAccountInTotal(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("存款", "bank", "CNY")
	b := e.accountID("信用卡", "bank", "CNY")
	e.addEntry("2024-01-01", a, 1000)
	e.addEntry("2024-01-01", b, -300)
	if got := e.summary("2024-01-01", "CNY").Total; got != 700 {
		t.Fatalf("负债应从总资产中扣减: %v", got)
	}
}

func TestC15_BadRange(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("GET", "/api/stats/timeseries?from=2024-02-01&to=2024-01-01&currency=CNY", nil), http.StatusBadRequest)
	e.code(e.do("GET", "/api/stats/compare?from=2024-02-01&to=2024-01-01&currency=CNY", nil), http.StatusBadRequest)
	e.code(e.do("GET", "/api/stats/timeseries?groupBy=week", nil), http.StatusBadRequest)
}

// C16 空数据时列表字段必须是 []，不能是 null（前端会在 .length 上崩溃）
func TestC16_NoNullArrays(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		path  string
		field string
	}{
		{"/api/stats/summary?date=2024-01-01&currency=CNY", "accounts"},
		{"/api/stats/summary?date=2024-01-01&currency=CNY", "categories"},
		{"/api/stats/timeseries?from=2024-01-01&to=2024-01-02&currency=CNY", "points"},
		{"/api/accounts", "items"},
		{"/api/entries", "items"},
	}
	for _, c := range cases {
		rec := e.code(e.do("GET", c.path, nil), http.StatusOK)
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatalf("解析失败 %s: %v", c.path, err)
		}
		got := string(raw[c.field])
		if got == "null" || got == "" {
			t.Fatalf("%s 的 %s 不应为 null，实际 %q", c.path, c.field, got)
		}
		if !strings.HasPrefix(got, "[") {
			t.Fatalf("%s 的 %s 应为数组，实际 %q", c.path, c.field, got)
		}
	}
}

// C17 只有归档账户时，汇总也应返回空数组
func TestC17_AllArchivedSummary(t *testing.T) {
	e := newEnv(t)
	id := e.accountID("销户卡", "bank", "CNY")
	e.addEntry("2024-01-01", id, 100)
	e.code(e.do("POST", "/api/accounts/"+itoa(id)+"/archive", map[string]any{"archived": true}), http.StatusOK)
	rec := e.code(e.do("GET", "/api/stats/summary?date="+model.Today()+"&currency=CNY", nil), http.StatusOK)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["accounts"]) != "[]" {
		t.Fatalf("全部归档时 accounts 应为 []，实际 %s", string(raw["accounts"]))
	}
}

// ---------- D. 导出 ----------

// csvRows 解析 CSV 行为二维切片。
func csvRows(body string) [][]string {
	body = strings.TrimPrefix(body, "\xEF\xBB\xBF")
	body = strings.TrimRight(body, "\r\n")
	if body == "" {
		return nil
	}
	lines := strings.Split(body, "\r\n")
	if len(lines) == 1 {
		lines = strings.Split(body, "\n")
	}
	rows := make([][]string, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, strings.Split(l, ","))
	}
	return rows
}

func TestD01_ExportEntriesCSV(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("招商银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	e.addEntry("2024-01-02", a, 200.5)
	rec := e.code(e.do("GET", "/api/export/entries?from=2024-01-01&to=2024-01-31", nil), http.StatusOK)
	rows := csvRows(rec.Body.String())
	if len(rows) != 3 {
		t.Fatalf("CSV 行数应为表头+2 条，实际 %d: %v", len(rows), rows)
	}
	if rows[0][0] != "日期" || rows[0][4] != "金额" {
		t.Fatalf("CSV 表头不正确: %v", rows[0])
	}
}

func TestD02_CSVAmountFormat(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 1234.5)
	rec := e.code(e.do("GET", "/api/export/entries", nil), http.StatusOK)
	rows := csvRows(rec.Body.String())
	if rows[1][4] != "1234.50" {
		t.Fatalf("CSV 金额格式不正确: %q", rows[1][4])
	}
}

func TestD03_CSVBOM(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 1)
	rec := e.code(e.do("GET", "/api/export/entries", nil), http.StatusOK)
	b := rec.Body.Bytes()
	if len(b) < 3 || b[0] != 0xEF || b[1] != 0xBB || b[2] != 0xBF {
		t.Fatalf("CSV 缺少 UTF-8 BOM")
	}
}

func TestD04_ExportSummaryCSV(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	rec := e.code(e.do("GET", "/api/export/summary?date=2024-01-01&currency=CNY", nil), http.StatusOK)
	rows := csvRows(rec.Body.String())
	if rows[0][0] != "类型" || rows[0][3] != "占比" {
		t.Fatalf("汇总 CSV 表头不正确: %v", rows[0])
	}
	if len(rows) < 3 {
		t.Fatalf("汇总 CSV 应含类别与账户行: %v", rows)
	}
}

func TestD05_ExportTimeseriesCSV(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	rec := e.code(e.do("GET", "/api/export/timeseries?from=2024-01-01&to=2024-01-03&currency=CNY", nil), http.StatusOK)
	rows := csvRows(rec.Body.String())
	if len(rows) != 4 || rows[0][0] != "日期" || rows[1][0] != "2024-01-01" || rows[3][0] != "2024-01-03" {
		t.Fatalf("走势 CSV 不正确: %v", rows)
	}
}

func TestD06_ExportJSON(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	rec := e.code(e.do("GET", "/api/export/entries?format=json", nil), http.StatusOK)
	var list []model.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("JSON 导出解析失败: %v | %s", err, rec.Body.String())
	}
	if len(list) != 1 || list[0].Amount != 100 {
		t.Fatalf("JSON 导出内容不正确: %+v", list)
	}
}

func TestD07_ExportEmpty(t *testing.T) {
	e := newEnv(t)
	rec := e.code(e.do("GET", "/api/export/entries", nil), http.StatusOK)
	rows := csvRows(rec.Body.String())
	if len(rows) != 1 {
		t.Fatalf("空数据导出应只有表头: %v", rows)
	}
}

func TestD08_ExportRangeFilter(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2023-12-01", a, 1)
	e.addEntry("2024-01-01", a, 2)
	rec := e.code(e.do("GET", "/api/export/entries?from=2024-01-01&to=2024-01-31", nil), http.StatusOK)
	rows := csvRows(rec.Body.String())
	if len(rows) != 2 {
		t.Fatalf("区间过滤导出不正确: %v", rows)
	}
}

func TestD09_BadExportFormat(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("GET", "/api/export/entries?format=xlsx", nil), http.StatusBadRequest)
}

// ---------- E. 备份与恢复 ----------

// snapshotOf 导出快照。
func (e *env) snapshotOf() model.Snapshot {
	e.t.Helper()
	rec := e.code(e.do("GET", "/api/backup/snapshot", nil), http.StatusOK)
	return decode[model.Snapshot](e, rec)
}

func TestE01_SnapshotContent(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	snap := e.snapshotOf()
	if snap.Version != model.SnapshotVersion || len(snap.Accounts) != 1 || len(snap.Entries) != 1 {
		t.Fatalf("快照内容不正确: %+v", snap)
	}
	if snap.Entries[0].Amount != 100 || snap.Entries[0].AccountName != "银行" {
		t.Fatalf("快照录入内容不正确: %+v", snap.Entries[0])
	}
}

func TestE02_RestoreReplace(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	snap := e.snapshotOf()

	e2 := newEnv(t)
	b := e2.accountID("无关账户", "bank", "CNY")
	_ = b
	e2.addEntry("2024-05-05", b, 888)
	e2.code(e2.do("POST", "/api/backup/restore", map[string]any{"mode": "replace", "payload": snap}), http.StatusOK)
	if got := e2.entriesOf("").Total; got != 1 {
		t.Fatalf("replace 后应只有快照中的 1 条，实际 %d", got)
	}
	list := e2.accountsOf("")
	if len(list) != 1 || list[0].Name != "银行" {
		t.Fatalf("replace 后账户不正确: %+v", list)
	}
	if got := e2.summary("2024-01-01", "CNY").Total; got != 100 {
		t.Fatalf("replace 后数据不正确: %v", got)
	}
}

func TestE03_RestoreMerge(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	snap := e.snapshotOf()

	e2 := newEnv(t)
	b := e2.accountID("银行", "bank", "CNY")
	_ = b
	e2.addEntry("2024-02-02", b, 500)
	e2.code(e2.do("POST", "/api/backup/restore", map[string]any{"mode": "merge", "payload": snap}), http.StatusOK)
	if got := e2.entriesOf("").Total; got != 2 {
		t.Fatalf("merge 后应有 2 条，实际 %d", got)
	}
	if got := e2.accountsOf(""); len(got) != 1 {
		t.Fatalf("merge 不应重复创建账户: %+v", got)
	}
}

func TestE04_MergeNewAccount(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("源账户", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	snap := e.snapshotOf()

	e2 := newEnv(t)
	e2.code(e2.do("POST", "/api/backup/restore", map[string]any{"mode": "merge", "payload": snap}), http.StatusOK)
	list := e2.accountsOf("")
	if len(list) != 1 || list[0].Name != "源账户" {
		t.Fatalf("merge 应自动创建新账户: %+v", list)
	}
}

func TestE05_RestoreInvalidPayload(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/backup/restore", map[string]any{"mode": "replace"}), http.StatusBadRequest)
	e.code(e.do("POST", "/api/backup/restore", map[string]any{
		"mode":    "replace",
		"payload": map[string]any{"version": model.SnapshotVersion},
	}), http.StatusBadRequest)
}

func TestE06_RestoreBadVersion(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/backup/restore", map[string]any{
		"mode":    "replace",
		"payload": map[string]any{"version": 999, "accounts": []any{}, "entries": []any{}},
	}), http.StatusBadRequest)
}

func TestE07_BackupToFile(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	dir := t.TempDir()
	rec := e.code(e.do("POST", "/api/backup/file", map[string]any{"dir": dir}), http.StatusOK)
	res := decode[map[string]any](e, rec)
	path, _ := res["path"].(string)
	if path == "" {
		t.Fatalf("备份返回路径为空: %v", res)
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		t.Fatalf("备份文件无效: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), "fam-") || !strings.HasSuffix(path, ".db") {
		t.Fatalf("备份文件命名不正确: %s", path)
	}
}

func TestE08_BackupFileEncrypted(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("招商银行储蓄卡", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	dir := t.TempDir()
	rec := e.code(e.do("POST", "/api/backup/file", map[string]any{"dir": dir}), http.StatusOK)
	path := decode[map[string]any](e, rec)["path"].(string)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw[:8]), "FAMVAULT") {
		t.Fatalf("备份应为加密容器，实际文件头: %q", string(raw[:8]))
	}
	if bytes.Contains(raw, []byte("SQLite format 3")) {
		t.Fatalf("备份中不应出现 SQLite 明文文件头")
	}
	if bytes.Contains(raw, []byte("招商银行储蓄卡")) {
		t.Fatalf("备份中不应出现明文账户名")
	}
}

func TestE09_RestoreFromFile(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	dir := t.TempDir()
	rec := e.code(e.do("POST", "/api/backup/file", map[string]any{"dir": dir}), http.StatusOK)
	path := decode[map[string]any](e, rec)["path"].(string)

	// 改动当前库后恢复
	e.addEntry("2024-06-06", a, 999)
	e.accountID("临时账户", "bank", "CNY")
	if e.entriesOf("").Total != 2 {
		t.Fatalf("恢复前数据不正确")
	}
	e.code(e.do("POST", "/api/backup/file/restore", map[string]any{"path": path}), http.StatusOK)
	if got := e.entriesOf("").Total; got != 1 {
		t.Fatalf("恢复后应为 1 条，实际 %d", got)
	}
	if got := e.accountsOf(""); len(got) != 1 || got[0].Name != "银行" {
		t.Fatalf("恢复后账户不正确: %+v", got)
	}
	// 恢复后当前密码依然有效
	e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": testPassword}), http.StatusOK)
	e.code(e.do("GET", "/api/health", nil), http.StatusOK)
}

func TestE10_RestoreMissingFile(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/backup/file/restore", map[string]any{"path": filepath.Join(t.TempDir(), "nope.db")}), http.StatusBadRequest)
	e.code(e.do("POST", "/api/backup/file/restore", map[string]any{}), http.StatusBadRequest)
}

func TestE11_RestoreCorruptFile(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	bad := filepath.Join(t.TempDir(), "bad.db")
	if err := os.WriteFile(bad, []byte("not a sqlite file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.code(e.do("POST", "/api/backup/file/restore", map[string]any{"path": bad}), http.StatusBadRequest)
	if got := e.entriesOf("").Total; got != 1 {
		t.Fatalf("恢复失败不应破坏现有数据")
	}
}

func TestE12_RestoreKeepsPassword(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	e.addEntry("2024-01-01", a, 100)
	dir := t.TempDir()
	rec := e.code(e.do("POST", "/api/backup/file", map[string]any{"dir": dir}), http.StatusOK)
	path := decode[map[string]any](e, rec)["path"].(string)
	e.code(e.do("POST", "/api/backup/file/restore", map[string]any{"path": path}), http.StatusOK)
	if _, err := os.Stat(e.st.Path()); err != nil {
		t.Fatalf("恢复后数据文件应存在: %v", err)
	}
	raw, err := os.ReadFile(e.st.Path())
	if err != nil || !strings.HasPrefix(string(raw[:8]), "FAMVAULT") {
		t.Fatalf("恢复后数据文件仍应为密文: %v", err)
	}
	e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": testPassword}), http.StatusOK)
}

func TestE13_RoundTrip(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("银行", "bank", "CNY")
	b := e.accountID("证券", "stock", "CNY")
	e.addEntry("2024-01-01", a, 100)
	e.addEntry("2024-02-01", b, 250.25)
	dir := t.TempDir()
	rec := e.code(e.do("POST", "/api/backup/file", map[string]any{"dir": dir}), http.StatusOK)
	path := decode[map[string]any](e, rec)["path"].(string)

	// 清空后从备份恢复，数据应完整回来
	e.code(e.do("POST", "/api/backup/restore", map[string]any{
		"mode":    "replace",
		"payload": map[string]any{"version": 1, "accounts": []any{}, "entries": []any{}},
	}), http.StatusOK)
	if e.entriesOf("").Total != 0 {
		t.Fatalf("清空后应为 0 条")
	}
	e.code(e.do("POST", "/api/backup/file/restore", map[string]any{"path": path}), http.StatusOK)
	if got := e.accountsOf(""); len(got) != 2 {
		t.Fatalf("往返后账户数不一致: %d", len(got))
	}
	if got := e.entriesOf("").Total; got != 2 {
		t.Fatalf("往返后录入数不一致: %d", got)
	}
	if got := e.summary("2024-03-01", "CNY").Total; math.Abs(got-350.25) > 1e-9 {
		t.Fatalf("往返后总额不一致: %v", got)
	}
}

// ---------- F. 系统能力 ----------

func TestF01_Health(t *testing.T) {
	e := newEnv(t)
	rec := e.code(e.do("GET", "/api/health", nil), http.StatusOK)
	res := decode[map[string]any](e, rec)
	if res["status"] != "ok" || res["dbPath"] == "" || res["dbPath"] == nil {
		t.Fatalf("健康检查响应不正确: %v", res)
	}
}

func TestF02_StaticIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>fam</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "fam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := New(st, dir)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "fam") {
		t.Fatalf("静态首页返回不正确: %d %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Fatalf("静态资源应要求重新校验，实际 Cache-Control=%q", cc)
	}
}

func TestF03_UnknownRoute(t *testing.T) {
	e := newEnv(t)
	rec := e.code(e.do("GET", "/api/not-exist", nil), http.StatusNotFound)
	if !strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("未知路由应返回 JSON 错误体: %s", rec.Body.String())
	}
}

func TestF04_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fam.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := New(st, "")
	setup := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(`{"password":"`+testPassword+`"}`))
	setupRec := httptest.NewRecorder()
	first.ServeHTTP(setupRec, setup)
	if setupRec.Code != http.StatusCreated {
		t.Fatalf("设置密码失败: %s", setupRec.Body.String())
	}
	var setupRes struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setupRes); err != nil || setupRes.Token == "" {
		t.Fatalf("未取得令牌: %s", setupRec.Body.String())
	}

	rec := httptest.NewRecorder()
	createReq := httptest.NewRequest("POST", "/api/accounts",
		strings.NewReader(`{"name":"持久化账户","category":"bank","currency":"CNY"}`))
	createReq.Header.Set("Authorization", "Bearer "+setupRes.Token)
	first.ServeHTTP(rec, createReq)
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建账户失败: %s", rec.Body.String())
	}
	st.Close()

	// 重启后：密码与数据都还在，需要重新登录
	st2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	h := New(st2, "")

	noAuth := httptest.NewRecorder()
	h.ServeHTTP(noAuth, httptest.NewRequest("GET", "/api/accounts", nil))
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatalf("重启后未登录应被拒绝，实际 %d", noAuth.Code)
	}

	loginRec := httptest.NewRecorder()
	h.ServeHTTP(loginRec, httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"`+testPassword+`"}`)))
	if loginRec.Code != http.StatusOK {
		t.Fatalf("重启后原密码应可登录，实际 %d: %s", loginRec.Code, loginRec.Body.String())
	}
	var loginRes struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginRes); err != nil || loginRes.Token == "" {
		t.Fatalf("登录未返回令牌: %s", loginRec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	getReq := httptest.NewRequest("GET", "/api/accounts", nil)
	getReq.Header.Set("Authorization", "Bearer "+loginRes.Token)
	h.ServeHTTP(rec2, getReq)
	if !strings.Contains(rec2.Body.String(), "持久化账户") {
		t.Fatalf("重启后数据丢失: %s", rec2.Body.String())
	}
}

func TestF05_BadJSONBody(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/accounts", "{"), http.StatusBadRequest)
}

func TestF06_MetaEndpoint(t *testing.T) {
	e := newEnv(t)
	rec := e.code(e.do("GET", "/api/meta/categories", nil), http.StatusOK)
	res := decode[map[string]any](e, rec)
	if res["categories"] == nil || res["currencies"] == nil {
		t.Fatalf("元数据接口不正确: %v", res)
	}
}

// ---------- I. 密码与鉴权 ----------

// authStatus 查询鉴权状态。
func (e *env) authStatus() map[string]any {
	e.t.Helper()
	rec := e.code(e.anonDo("GET", "/api/auth/status", nil), http.StatusOK)
	return decode[map[string]any](e, rec)
}

func TestI01_StatusBeforeSetup(t *testing.T) {
	e := newEnvBare(t)
	res := e.authStatus()
	if res["initialized"] != false {
		t.Fatalf("首次启动应为未初始化: %v", res)
	}
}

func TestI02_ProtectedBeforeSetup(t *testing.T) {
	e := newEnvBare(t)
	rec := e.anonDo("GET", "/api/accounts", nil)
	e.code(rec, http.StatusUnauthorized)
	if !strings.Contains(rec.Body.String(), "NOT_INITIALIZED") {
		t.Fatalf("未初始化时应返回 NOT_INITIALIZED: %s", rec.Body.String())
	}
	// 静态页面仍可访问（需要显示设置密码界面）
	e.code(e.anonDo("GET", "/api/health", nil), http.StatusOK)
}

func TestI03_SetupWeakPassword(t *testing.T) {
	e := newEnvBare(t)
	e.code(e.anonDo("POST", "/api/auth/setup", map[string]any{"password": "123"}), http.StatusBadRequest)
	e.code(e.anonDo("POST", "/api/auth/setup", map[string]any{"password": strings.Repeat("x", 73)}), http.StatusBadRequest)
	e.code(e.anonDo("POST", "/api/auth/setup", map[string]any{}), http.StatusBadRequest)
}

func TestI04_SetupSuccess(t *testing.T) {
	e := newEnvBare(t)
	rec := e.code(e.anonDo("POST", "/api/auth/setup", map[string]any{"password": testPassword}), http.StatusCreated)
	res := decode[map[string]any](e, rec)
	if token, _ := res["token"].(string); token == "" {
		t.Fatalf("设置密码应返回令牌: %v", res)
	}
	if e.authStatus()["initialized"] != true {
		t.Fatalf("设置后应标记为已初始化")
	}
}

func TestI05_SetupTwice(t *testing.T) {
	e := newEnv(t)
	e.code(e.anonDo("POST", "/api/auth/setup", map[string]any{"password": "another123"}), http.StatusConflict)
}

func TestI06_NoTokenRejected(t *testing.T) {
	e := newEnv(t)
	e.code(e.anonDo("GET", "/api/accounts", nil), http.StatusUnauthorized)
	e.code(e.anonDo("GET", "/api/stats/summary", nil), http.StatusUnauthorized)
	e.code(e.do("GET", "/api/accounts", nil), http.StatusOK)
}

func TestI07_WrongPassword(t *testing.T) {
	e := newEnv(t)
	rec := e.anonDo("POST", "/api/auth/login", map[string]any{"password": "wrong-pass"})
	e.code(rec, http.StatusUnauthorized)
	if !strings.Contains(rec.Body.String(), "BAD_PASSWORD") {
		t.Fatalf("错误密码应返回 BAD_PASSWORD: %s", rec.Body.String())
	}
}

func TestI08_LoginSuccess(t *testing.T) {
	e := newEnv(t)
	rec := e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": testPassword}), http.StatusOK)
	token := decode[map[string]any](e, rec)["token"].(string)
	if token == "" {
		t.Fatalf("登录应返回令牌")
	}
	saved := e.token
	e.token = token
	e.code(e.do("GET", "/api/accounts", nil), http.StatusOK)
	e.token = saved
}

func TestI09_LogoutRevokesToken(t *testing.T) {
	e := newEnv(t)
	e.code(e.do("POST", "/api/auth/logout", nil), http.StatusOK)
	e.code(e.do("GET", "/api/accounts", nil), http.StatusUnauthorized)
}

func TestI10_ForgedTokenRejected(t *testing.T) {
	e := newEnv(t)
	saved := e.token
	e.token = strings.Repeat("a", 64)
	e.code(e.do("GET", "/api/accounts", nil), http.StatusUnauthorized)
	e.token = saved
	e.code(e.do("GET", "/api/accounts", nil), http.StatusOK)
}

func TestI11_ChangePassword(t *testing.T) {
	e := newEnv(t)
	oldToken := e.token
	e.code(e.do("POST", "/api/auth/password", map[string]any{
		"oldPassword": "wrong-old", "newPassword": "newpass123",
	}), http.StatusUnauthorized)
	e.code(e.do("POST", "/api/auth/password", map[string]any{
		"oldPassword": testPassword, "newPassword": "123",
	}), http.StatusBadRequest)

	rec := e.code(e.do("POST", "/api/auth/password", map[string]any{
		"oldPassword": testPassword, "newPassword": "newpass123",
	}), http.StatusOK)
	newToken := decode[map[string]any](e, rec)["token"].(string)
	if newToken == "" {
		t.Fatalf("修改密码应返回新令牌")
	}

	// 旧令牌失效
	e.token = oldToken
	e.code(e.do("GET", "/api/accounts", nil), http.StatusUnauthorized)

	// 新密码可登录，旧密码不可
	e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": testPassword}), http.StatusUnauthorized)
	e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": "newpass123"}), http.StatusOK)
}

func TestI12_LoginThrottle(t *testing.T) {
	e := newEnv(t)
	e.remote = "10.9.9.9:5555"
	for i := 0; i < 4; i++ {
		e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": "wrong-pass"}), http.StatusUnauthorized)
	}
	e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": "wrong-pass"}), http.StatusTooManyRequests)
	// 即使密码正确，锁定期内也拒绝
	e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": testPassword}), http.StatusTooManyRequests)
}

func TestI13_DataFileEncrypted(t *testing.T) {
	e := newEnv(t)
	e.accountID("招商银行储蓄卡", "bank", "CNY")
	raw, err := os.ReadFile(e.st.Path())
	if err != nil {
		t.Fatalf("读取数据文件失败: %v", err)
	}
	if !strings.HasPrefix(string(raw[:8]), "FAMVAULT") {
		t.Fatalf("数据文件应为加密容器，实际文件头: %q", string(raw[:8]))
	}
	if bytes.Contains(raw, []byte("SQLite format 3")) {
		t.Fatalf("数据文件中不应出现 SQLite 明文文件头")
	}
	if bytes.Contains(raw, []byte("招商银行储蓄卡")) {
		t.Fatalf("数据文件中不应出现明文账户名")
	}
	if bytes.Contains(raw, []byte(testPassword)) {
		t.Fatalf("数据文件中不应出现明文密码")
	}
}

// ---------- K. 加密存储 ----------

func TestK01_LockedAfterRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fam.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Setup(testPassword); err != nil {
		t.Fatalf("设置密码失败: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO accounts(name,category,currency,note,archived,created_at,updated_at)
		VALUES('加密账户','bank','CNY','',0,'x','x')`); err != nil {
		t.Fatal(err)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if !st2.Initialized() {
		t.Fatalf("重启后应识别为已初始化")
	}
	if st2.Unlocked() {
		t.Fatalf("重启后应处于锁定状态")
	}
	if err := st2.Unlock("wrong-pass"); err == nil {
		t.Fatalf("错误密码不应解锁")
	}
	if err := st2.Unlock(testPassword); err != nil {
		t.Fatalf("正确密码应解锁: %v", err)
	}
	list, err := st2.ListAccounts(store.AccountFilter{})
	if err != nil || len(list) != 1 || list[0].Name != "加密账户" {
		t.Fatalf("解锁后数据不正确: %v %+v", err, list)
	}
}

func TestK02_LockClearsMemory(t *testing.T) {
	e := newEnv(t)
	e.accountID("账户A", "bank", "CNY")
	e.code(e.do("POST", "/api/auth/logout", nil), http.StatusOK)
	// 锁定后数据接口应拒绝
	rec := e.code(e.do("GET", "/api/accounts", nil), http.StatusUnauthorized)
	if !strings.Contains(rec.Body.String(), "LOCKED") {
		t.Fatalf("锁定后应返回 LOCKED: %s", rec.Body.String())
	}
	// 重新解锁后数据仍在
	rec2 := e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": testPassword}), http.StatusOK)
	token := decode[map[string]any](e, rec2)["token"].(string)
	e.token = token
	if got := e.accountsOf(""); len(got) != 1 {
		t.Fatalf("解锁后数据丢失: %+v", got)
	}
}

func TestK03_NoPlaintextAfterWrite(t *testing.T) {
	e := newEnv(t)
	a := e.accountID("工商银行工资卡", "bank", "CNY")
	e.addEntry("2024-01-01", a, 88888.88)
	e.code(e.do("POST", "/api/backup/file", map[string]any{"dir": t.TempDir()}), http.StatusOK)

	raw, err := os.ReadFile(e.st.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"工商银行工资卡", "88888", "balance_entries"} {
		if bytes.Contains(raw, []byte(needle)) {
			t.Fatalf("数据文件中不应出现明文内容: %s", needle)
		}
	}
}

func TestK06_TamperDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fam.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Setup(testPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO accounts(name,category,currency,note,archived,created_at,updated_at)
		VALUES('账户','bank','CNY','',0,'x','x')`); err != nil {
		t.Fatal(err)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	st.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xFF
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	st2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	h := New(st2, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"`+testPassword+`"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("篡改后解锁应返回 400，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "CORRUPT") {
		t.Fatalf("应返回 CORRUPT 错误: %s", rec.Body.String())
	}
}

func TestI14_StatusIsPublic(t *testing.T) {
	e := newEnv(t)
	e.code(e.anonDo("GET", "/api/health", nil), http.StatusOK)
	e.code(e.anonDo("GET", "/api/auth/status", nil), http.StatusOK)
	// 元数据接口受保护
	e.code(e.anonDo("GET", "/api/meta/categories", nil), http.StatusUnauthorized)
}

func TestI15_DifferentPasswordSameDB(t *testing.T) {
	e := newEnvBare(t)
	e.setupPassword("first-pass")
	// 第二次设置会被拒绝，密码保持不变
	e.code(e.anonDo("POST", "/api/auth/setup", map[string]any{"password": "second-pass"}), http.StatusConflict)
	e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": "first-pass"}), http.StatusOK)
	e.code(e.anonDo("POST", "/api/auth/login", map[string]any{"password": "second-pass"}), http.StatusUnauthorized)
}

// itoa int64 转字符串。
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
