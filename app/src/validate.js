/**
 * 前端输入校验：与后端规则保持一致，同时可在 Node 中直接测试。
 */
(function (root, factory) {
  const mod = factory();
  if (typeof module === 'object' && module.exports) {
    module.exports = mod;
  } else {
    root.FamValidate = mod;
  }
})(typeof self !== 'undefined' ? self : this, function () {
  const MAX_AMOUNT = 1e12;

  function ok(value) {
    return { ok: true, value };
  }
  function fail(message) {
    return { ok: false, message };
  }

  /** 判断字符串是否为空（含纯空白）。 */
  function isBlank(v) {
    return typeof v !== 'string' || v.trim() === '';
  }

  /** 校验账户名称：必填，1–50 个字符。 */
  function accountName(v) {
    if (isBlank(v)) return fail('账户名称不能为空');
    const name = v.trim();
    if (Array.from(name).length > 50) return fail('账户名称不能超过 50 个字符');
    return ok(name);
  }

  /** 校验备注：最多 200 个字符。 */
  function note(v) {
    const text = typeof v === 'string' ? v.trim() : '';
    if (Array.from(text).length > 200) return fail('备注不能超过 200 个字符');
    return ok(text);
  }

  /** 校验金额：数字、最多两位小数、绝对值不超过 1 万亿。 */
  function amount(v) {
    if (typeof v === 'number') {
      if (!isFinite(v)) return fail('金额不是有效数字');
      if (Math.abs(v) > MAX_AMOUNT) return fail('金额超出允许范围（最大 1 万亿）');
      if (Math.abs(Math.round(v * 100) - v * 100) > 1e-6) return fail('金额最多保留两位小数');
      return ok(Math.round(v * 100));
    }
    if (isBlank(String(v))) return fail('金额不能为空');
    const raw = String(v).trim();
    if (!/^-?\d+(\.\d{1,2})?$/.test(raw)) return fail('金额格式不正确，最多两位小数');
    const num = Number(raw);
    if (Math.abs(num) > MAX_AMOUNT) return fail('金额超出允许范围（最大 1 万亿）');
    return ok(Math.round(num * 100));
  }

  /** 校验日期：YYYY-MM-DD，合法日期，不得晚于今天。 */
  function date(v, today) {
    if (isBlank(String(v))) return fail('日期不能为空');
    const raw = String(v).trim();
    if (!/^\d{4}-\d{2}-\d{2}$/.test(raw)) return fail('日期格式无效，应为 YYYY-MM-DD');
    const [y, m, d] = raw.split('-').map(Number);
    const dt = new Date(y, m - 1, d);
    if (dt.getFullYear() !== y || dt.getMonth() !== m - 1 || dt.getDate() !== d) {
      return fail('日期不存在');
    }
    const ref = today || todayStr();
    if (raw > ref) return fail('录入日期不能晚于今天');
    return ok(raw);
  }

  /** 校验日期区间：起止均合法且 from ≤ to。 */
  function range(from, to, today) {
    const f = date(from, today);
    if (!f.ok) return f;
    const t = date(to, today);
    if (!t.ok) return t;
    if (f.value > t.value) return fail('开始日期不能晚于结束日期');
    return ok({ from: f.value, to: t.value });
  }

  /** 本地日期字符串 YYYY-MM-DD。 */
  function todayStr() {
    const d = new Date();
    const p = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
  }

  /** 金额（分）转十进制数。 */
  function fromMinor(minor) {
    return Math.round(minor) / 100;
  }

  /** 校验访问密码：6–72 字符，且不能全为空格。 */
  function password(v, minLength) {
    const min = minLength || 6;
    const raw = typeof v === 'string' ? v : '';
    const len = Array.from(raw).length;
    if (len === 0) return fail('密码不能为空');
    if (len < min) return fail('密码至少需要 ' + min + ' 个字符');
    if (len > 72) return fail('密码不能超过 72 个字符');
    if (raw.trim() === '') return fail('密码不能全为空格');
    return ok(raw);
  }

  /** 校验设置密码时的两次输入。 */
  function passwordPair(pw, pw2) {
    const first = password(pw);
    if (!first.ok) return first;
    if (pw !== pw2) return fail('两次输入的密码不一致');
    return ok(pw);
  }

  return {
    isBlank,
    accountName,
    note,
    amount,
    date,
    range,
    todayStr,
    fromMinor,
    password,
    passwordPair,
    MAX_AMOUNT,
  };
});
