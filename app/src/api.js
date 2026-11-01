/**
 * 后端接口封装：所有请求走本机 127.0.0.1 的随机端口，并携带会话令牌。
 */
(function (root) {
  const base = (root.__FAM_API_BASE__ !== undefined ? root.__FAM_API_BASE__ : '') + '/api';
  const TOKEN_KEY = 'fam_token';

  let token = '';
  try {
    token = sessionStorage.getItem(TOKEN_KEY) || '';
  } catch (e) {
    token = '';
  }

  let onUnauthorized = null;

  /** 统一的请求错误。 */
  class ApiError extends Error {
    constructor(status, code, message) {
      super(message || '请求失败');
      this.status = status;
      this.code = code;
    }
  }

  function setToken(t) {
    token = t || '';
    try {
      if (token) sessionStorage.setItem(TOKEN_KEY, token);
      else sessionStorage.removeItem(TOKEN_KEY);
    } catch (e) {
      /* 忽略存储异常 */
    }
  }

  function hasToken() {
    return token !== '';
  }

  async function request(method, path, body) {
    const init = { method, headers: {} };
    if (token) init.headers.Authorization = 'Bearer ' + token;
    if (body !== undefined && body !== null) {
      init.headers['Content-Type'] = 'application/json';
      init.body = JSON.stringify(body);
    }
    let res;
    try {
      res = await fetch(base + path, init);
    } catch (e) {
      throw new ApiError(0, 'NETWORK', '无法连接本机服务，请重启应用');
    }
    const isJson = (res.headers.get('content-type') || '').includes('application/json');
    const data = isJson ? await res.json() : await res.text();
    if (!res.ok) {
      const err = (data && data.error) || {};
      // 登录类接口的鉴权失败由调用方处理，其余接口统一回到锁屏
      const isAuthPath = path.startsWith('/auth/');
      if (res.status === 401 && !isAuthPath && typeof onUnauthorized === 'function') {
        onUnauthorized(err.code || 'UNAUTHORIZED');
      }
      throw new ApiError(res.status, err.code || 'ERROR', err.message || `请求失败（${res.status}）`);
    }
    return data;
  }

  /** 下载导出文件（CSV / JSON）。 */
  async function download(path, filename) {
    const headers = {};
    if (token) headers.Authorization = 'Bearer ' + token;
    const res = await fetch(base + path, { headers });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      const err = data.error || {};
      throw new ApiError(res.status, err.code || 'ERROR', err.message || '导出失败');
    }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  root.FamAPI = {
    ApiError,
    setToken,
    hasToken,
    onUnauthorized: (fn) => {
      onUnauthorized = fn;
    },

    authStatus: () => request('GET', '/auth/status'),
    setupPassword: (password) => request('POST', '/auth/setup', { password }),
    login: (password) => request('POST', '/auth/login', { password }),
    logout: () => request('POST', '/auth/logout', {}),
    changePassword: (oldPassword, newPassword) => request('POST', '/auth/password', { oldPassword, newPassword }),

    health: () => request('GET', '/health'),
    meta: () => request('GET', '/meta/categories'),

    listAccounts: (includeArchived, keyword) =>
      request('GET', `/accounts?includeArchived=${includeArchived ? 'true' : 'false'}${keyword ? '&keyword=' + encodeURIComponent(keyword) : ''}`),
    createAccount: (payload) => request('POST', '/accounts', payload),
    updateAccount: (id, payload) => request('PUT', `/accounts/${id}`, payload),
    archiveAccount: (id, archived) => request('POST', `/accounts/${id}/archive`, { archived }),

    listEntries: (params) => request('GET', '/entries' + (params || '')),
    createEntries: (date, items) => request('POST', '/entries', { date, items }),

    updateEntry: (id, payload) => request('PUT', `/entries/${id}`, payload),
    deleteEntry: (id) => request('DELETE', `/entries/${id}`),

    summary: (date, currency) => request('GET', `/stats/summary?date=${date}&currency=${currency}`),
    timeseries: (from, to, currency, groupBy) =>
      request('GET', `/stats/timeseries?from=${from}&to=${to}&currency=${currency}&groupBy=${groupBy || 'day'}`),
    compare: (from, to, currency) => request('GET', `/stats/compare?from=${from}&to=${to}&currency=${currency}`),

    exportEntries: (from, to) => download(`/export/entries?from=${from}&to=${to}`, `fam-录入明细-${from || '全部'}-${to}.csv`),
    exportSummary: (date) => download(`/export/summary?date=${date}`, `fam-汇总-${date}.csv`),
    exportTimeseries: (from, to) => download(`/export/timeseries?from=${from}&to=${to}&currency=CNY`, `fam-走势-${from}-${to}.csv`),

    snapshot: () => request('GET', '/backup/snapshot'),
    restore: (mode, payload) => request('POST', '/backup/restore', { mode, payload }),
    backupFile: (dir) => request('POST', '/backup/file', dir ? { dir } : {}),
    restoreFile: (path) => request('POST', '/backup/file/restore', { path }),
  };
})(typeof window !== 'undefined' ? window : this);
