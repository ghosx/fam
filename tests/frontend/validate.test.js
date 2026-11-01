/**
 * 前端校验逻辑测试：node --test tests/frontend
 */
const test = require('node:test');
const assert = require('node:assert');
const V = require('../../app/src/validate.js');

const TODAY = '2026-11-01';

// G01 账户名称必填
test('G01 账户名称必填', () => {
  assert.strictEqual(V.accountName('').ok, false);
  assert.strictEqual(V.accountName('   ').ok, false);
  assert.strictEqual(V.accountName('招商银行').ok, true);
});

// G02 名称长度边界
test('G02 名称长度边界 50/51', () => {
  assert.strictEqual(V.accountName('x'.repeat(50)).ok, true);
  assert.strictEqual(V.accountName('x'.repeat(51)).ok, false);
});

// G03 合法金额
test('G03 合法金额格式', () => {
  assert.strictEqual(V.amount('1234.56').ok, true);
  assert.strictEqual(V.amount('0').ok, true);
  assert.strictEqual(V.amount('0.5').ok, true);
});

// G04 非法金额
test('G04 非法金额被拒绝', () => {
  assert.strictEqual(V.amount('abc').ok, false);
  assert.strictEqual(V.amount('').ok, false);
  assert.strictEqual(V.amount('1.2.3').ok, false);
});

// G05 三位小数
test('G05 金额最多两位小数', () => {
  assert.strictEqual(V.amount('1.234').ok, false);
  assert.strictEqual(V.amount('1.23').ok, true);
});

// G06 负金额
test('G06 负金额允许', () => {
  const r = V.amount('-500');
  assert.strictEqual(r.ok, true);
  assert.strictEqual(r.value, -50000);
});

// G07 日期格式与合法性
test('G07 日期格式与合法性', () => {
  assert.strictEqual(V.date('2024-01-01', TODAY).ok, true);
  assert.strictEqual(V.date('2024-13-01', TODAY).ok, false);
  assert.strictEqual(V.date('2024-02-30', TODAY).ok, false);
  assert.strictEqual(V.date('2024/01/01', TODAY).ok, false);
  assert.strictEqual(V.date('2024-02-29', TODAY).ok, true, '闰年 2 月 29 日应合法');
});

// G08 未来日期
test('G08 未来日期被拒绝', () => {
  assert.strictEqual(V.date('2099-01-01', TODAY).ok, false);
  assert.strictEqual(V.date('2026-11-01', TODAY).ok, true);
  assert.strictEqual(V.date('2026-11-02', TODAY).ok, false);
});

// G09 区间顺序
test('G09 日期区间顺序', () => {
  assert.strictEqual(V.range('2024-01-01', '2024-02-01', TODAY).ok, true);
  const bad = V.range('2024-03-01', '2024-02-01', TODAY);
  assert.strictEqual(bad.ok, false);
  assert.match(bad.message, /开始日期/);
});

// G10 备注长度边界
test('G10 备注长度边界 200/201', () => {
  assert.strictEqual(V.note('x'.repeat(200)).ok, true);
  assert.strictEqual(V.note('x'.repeat(201)).ok, false);
});

// G11 金额转分
test('G11 金额转分（两位小数整数化）', () => {
  assert.strictEqual(V.amount('10.23').value, 1023);
  assert.strictEqual(V.amount('-10.23').value, -1023);
  assert.strictEqual(V.amount('0.01').value, 1);
  assert.strictEqual(V.amount('1.004').ok, false, '三位小数应被拒绝');
  assert.strictEqual(V.fromMinor(1023), 10.23);
});

// G12 金额上限
test('G12 金额上限', () => {
  assert.strictEqual(V.amount('1000000000000').ok, true);
  assert.strictEqual(V.amount('1000000000001').ok, false);
});

// G13 本地日期字符串
test('G13 todayStr 返回 YYYY-MM-DD', () => {
  assert.match(V.todayStr(), /^\d{4}-\d{2}-\d{2}$/);
});

// G14 密码长度边界
test('G14 密码长度边界', () => {
  assert.strictEqual(V.password('12345').ok, false);
  assert.strictEqual(V.password('123456').ok, true);
  assert.strictEqual(V.password('x'.repeat(72)).ok, true);
  assert.strictEqual(V.password('x'.repeat(73)).ok, false);
});

// G15 密码不能为空或全空格
test('G15 空密码与全空格被拒绝', () => {
  assert.strictEqual(V.password('').ok, false);
  assert.strictEqual(V.password('      ').ok, false);
});

// G16 两次密码一致性
test('G16 两次输入的密码必须一致', () => {
  assert.strictEqual(V.passwordPair('abc12345', 'abc12345').ok, true);
  const bad = V.passwordPair('abc12345', 'abc12346');
  assert.strictEqual(bad.ok, false);
  assert.match(bad.message, /不一致/);
});
