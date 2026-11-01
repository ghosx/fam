/**
 * fam 前端主逻辑：账户 / 录入 / 分析 / 数据 四个页签。
 */
(function () {
  const API = window.FamAPI;
  const V = window.FamValidate;
  const C = window.FamCharts;

  const state = {
    meta: null,
    accounts: [],
    editingAccountId: null,
    editingEntryId: null,
    encrypted: true,
  };

  const $ = (id) => document.getElementById(id);
  /** 保证拿到数组（后端历史上可能返回 null）。 */
  const arr = (v) => (Array.isArray(v) ? v : []);
  const money = (v) => (v === null || v === undefined ? '—' : C.money(v));

  /** 更新右上角状态指示器。kind: ok | locked | error */
  function setStatus(kind, text) {
    const el = $('status');
    el.className = 'status ' + kind;
    el.textContent = text;
  }

  function toast(message, isError) {
    const el = $('toast');
    el.textContent = message;
    el.className = 'toast' + (isError ? ' error' : '');
    el.classList.remove('hidden');
    clearTimeout(el._timer);
    el._timer = setTimeout(() => el.classList.add('hidden'), isError ? 5000 : 2500);
  }

  function errorOf(err) {
    return err && err.message ? err.message : String(err);
  }

  // ---------- 初始化 ----------

  async function boot() {
    bindTabs();
    bindAccounts();
    bindEntries();
    bindAnalysis();
    bindData();
    window.FamLock.init({ onUnlocked: startApp });

    try {
      const st = await API.authStatus();
      state.encrypted = st.encrypted !== false;
      if (!st.initialized) {
        setStatus('locked', '待设置访问密码');
        window.FamLock.show('setup');
        return;
      }
      if (!st.authenticated) {
        setStatus('locked', '已锁定');
        window.FamLock.show('unlock');
        return;
      }
      await startApp();
    } catch (err) {
      setStatus('error', '服务未连接');
      window.FamLock.show('unlock');
      $('lock-subtitle').textContent = '无法连接本机服务，请重启应用后重试';
      toast(errorOf(err), true);
    }
  }

  /** 解锁成功后加载数据。 */
  async function startApp() {
    try {
      // 重载页面时令牌仍可能有效，此时必须主动收起锁屏遮罩
      window.FamLock.hide();
      const health = await API.health();
      setStatus('ok', `服务正常 · ${state.encrypted ? 'AES-256 加密' : '未加密'} · v${health.version}`);
      if (!state.meta) {
        state.meta = await API.meta();
        fillSelects();
        const today = V.todayStr();
        $('entry-date').value = today;
        $('entry-date').max = today;
        $('ana-date').value = today;
        $('ana-to').value = today;
        $('ana-from').value = shiftMonths(today, -1);
        $('exp-to').value = today;
        $('exp-from').value = shiftMonths(today, -1);
      }
      await loadAccounts();
      await loadQuick();
      await loadHistory();
      await refreshAnalysis();
    } catch (err) {
      toast(errorOf(err), true);
    }
  }

  function bindTabs() {
    document.querySelectorAll('.tab').forEach((btn) => {
      btn.addEventListener('click', () => {
        document.querySelectorAll('.tab').forEach((b) => b.classList.remove('active'));
        document.querySelectorAll('.view').forEach((v) => v.classList.remove('active'));
        btn.classList.add('active');
        $('view-' + btn.dataset.tab).classList.add('active');
        // 隐藏状态下创建的图表尺寸为 0，切回可见后需要重新计算
        if (btn.dataset.tab === 'analysis') setTimeout(() => C.resizeAll(), 0);
      });
    });
    $('lock-now').addEventListener('click', async () => {
      setStatus('locked', '已锁定');
      await window.FamLock.lockNow();
    });
  }

  function fillSelects() {
    const cats = state.meta.categories.map((c) => ({ value: c, label: state.meta.categoryLabels[c] }));
    fillOptions($('acct-form').category, cats);
    fillOptions($('ana-currency'), state.meta.currencies.map((c) => ({ value: c, label: c })));
    fillOptions($('acct-form').currency, state.meta.currencies.map((c) => ({ value: c, label: c })));
    $('ana-currency').value = 'CNY';
    $('acct-form').category.value = 'bank';
    $('acct-form').currency.value = 'CNY';
  }

  function fillOptions(select, items) {
    select.innerHTML = '';
    items.forEach((i) => {
      const o = document.createElement('option');
      o.value = i.value;
      o.textContent = i.label;
      select.appendChild(o);
    });
  }

  function shiftMonths(dateStr, months) {
    const [y, m, d] = dateStr.split('-').map(Number);
    const dt = new Date(y, m - 1 + months, d);
    if (dt.getMonth() !== ((m - 1 + months) % 12 + 12) % 12) dt.setDate(0);
    const p = (n) => String(n).padStart(2, '0');
    return `${dt.getFullYear()}-${p(dt.getMonth() + 1)}-${p(dt.getDate())}`;
  }

  // ---------- 账户 ----------

  function bindAccounts() {
    $('acct-new').addEventListener('click', () => openAccountModal(null));
    $('acct-cancel').addEventListener('click', () => $('acct-modal').classList.add('hidden'));
    $('acct-keyword').addEventListener('input', loadAccounts);
    $('acct-show-archived').addEventListener('change', loadAccounts);
    $('acct-form').addEventListener('submit', submitAccount);
  }

  async function loadAccounts() {
    try {
      const res = await API.listAccounts($('acct-show-archived').checked, $('acct-keyword').value.trim());
      state.accounts = arr(res.items);
      renderAccounts(state.accounts);
      refreshAccountOptions();
    } catch (err) {
      toast(errorOf(err), true);
    }
  }

  function renderAccounts(items) {
    const body = $('acct-body');
    body.innerHTML = '';
    $('acct-empty').classList.toggle('hidden', items.length > 0);
    items.forEach((a) => {
      const tr = document.createElement('tr');
      if (a.archived) tr.className = 'archived';
      tr.innerHTML = `
        <td>${escapeHtml(a.name)}</td>
        <td>${state.meta.categoryLabels[a.category] || a.category}</td>
        <td>${a.currency}</td>
        <td>${escapeHtml(a.note || '')}</td>
        <td class="num">${a.latestAmount === null || a.latestAmount === undefined ? '—' : money(a.latestAmount)}</td>
        <td>${a.latestDate || '—'}</td>
        <td><span class="tag ${a.archived ? '' : 'on'}">${a.archived ? '已归档' : '有效'}</span></td>
        <td class="ops">
          <button class="link" data-edit="${a.id}">编辑</button>
          <button class="link" data-archive="${a.id}" data-next="${a.archived ? 'false' : 'true'}">${a.archived ? '恢复' : '归档'}</button>
        </td>`;
      body.appendChild(tr);
    });
    body.querySelectorAll('[data-edit]').forEach((b) => b.addEventListener('click', () => {
      openAccountModal(items.find((a) => a.id === Number(b.dataset.edit)));
    }));
    body.querySelectorAll('[data-archive]').forEach((b) => b.addEventListener('click', async () => {
      try {
        await API.archiveAccount(Number(b.dataset.archive), b.dataset.next === 'true');
        await loadAccounts();
        await loadQuick();
        await refreshAnalysis();
      } catch (err) {
        toast(errorOf(err), true);
      }
    }));
  }

  function refreshAccountOptions() {
    const sel = $('hist-account');
    const cur = sel.value;
    sel.innerHTML = '<option value="">全部</option>';
    state.accounts.filter((a) => !a.archived).forEach((a) => {
      const o = document.createElement('option');
      o.value = a.id;
      o.textContent = `${a.name}（${a.currency}）`;
      sel.appendChild(o);
    });
    sel.value = cur;
  }

  function openAccountModal(account) {
    state.editingAccountId = account ? account.id : null;
    $('acct-modal-title').textContent = account ? '编辑账户' : '新增账户';
    const f = $('acct-form');
    f.name.value = account ? account.name : '';
    f.category.value = account ? account.category : 'bank';
    f.currency.value = account ? account.currency : 'CNY';
    f.note.value = account ? account.note || '' : '';
    $('acct-error').classList.add('hidden');
    $('acct-modal').classList.remove('hidden');
    f.name.focus();
  }

  async function submitAccount(e) {
    e.preventDefault();
    const f = $('acct-form');
    const errEl = $('acct-error');
    const name = V.accountName(f.name.value);
    const note = V.note(f.note.value);
    if (!name.ok) return showError(errEl, name.message);
    if (!note.ok) return showError(errEl, note.message);
    errEl.classList.add('hidden');
    const payload = { name: name.value, category: f.category.value, currency: f.currency.value, note: note.value };
    try {
      if (state.editingAccountId) {
        await API.updateAccount(state.editingAccountId, payload);
      } else {
        await API.createAccount(payload);
      }
      $('acct-modal').classList.add('hidden');
      await loadAccounts();
      await loadQuick();
      toast(state.editingAccountId ? '账户已更新' : '账户已创建');
    } catch (err) {
      showError(errEl, errorOf(err));
    }
  }

  function showError(el, msg) {
    el.textContent = msg;
    el.classList.remove('hidden');
  }

  // ---------- 录入 ----------

  function bindEntries() {
    $('entry-save').addEventListener('click', saveEntries);
    $('hist-search').addEventListener('click', loadHistory);
    $('entry-date').addEventListener('change', loadQuick);
  }

  async function loadQuick() {
    const date = $('entry-date').value || V.todayStr();
    try {
      const res = await API.listAccounts(false, '');
      const active = arr(res.items).filter((a) => !a.archived);
      const body = $('quick-body');
      body.innerHTML = '';
      $('quick-empty').classList.toggle('hidden', active.length > 0);
      active.forEach((a) => {
        const tr = document.createElement('tr');
        tr.innerHTML = `
          <td>${escapeHtml(a.name)} <span class="tag">${a.currency}</span></td>
          <td>${state.meta.categoryLabels[a.category] || a.category}</td>
          <td class="num"><input type="text" inputmode="decimal" placeholder="0.00" data-amount="${a.id}" /></td>
          <td><input type="text" maxlength="200" data-note="${a.id}" placeholder="选填" /></td>`;
        body.appendChild(tr);
      });
      // 预填上一次的余额，便于在原有基础上修改
      if (active.length) {
        const page = await API.listEntries(`?to=${date}&pageSize=500`);
        const lastByAccount = {};
        page.items.forEach((e) => {
          if (!lastByAccount[e.accountId]) lastByAccount[e.accountId] = e.amount;
        });
        active.forEach((a) => {
          const input = body.querySelector(`[data-amount="${a.id}"]`);
          if (input && lastByAccount[a.id] !== undefined) input.placeholder = lastByAccount[a.id].toFixed(2);
        });
      }
    } catch (err) {
      toast(errorOf(err), true);
    }
  }

  async function saveEntries() {
    const dateRes = V.date($('entry-date').value);
    if (!dateRes.ok) return toast(dateRes.message, true);

    const items = [];
    let firstError = null;
    document.querySelectorAll('[data-amount]').forEach((input) => {
      const raw = input.value.trim();
      if (raw === '') return;
      const res = V.amount(raw);
      if (!res.ok) {
        if (!firstError) firstError = res.message;
        return;
      }
      const noteInput = document.querySelector(`[data-note="${input.dataset.amount}"]`);
      const noteRes = V.note(noteInput ? noteInput.value : '');
      if (!noteRes.ok) {
        if (!firstError) firstError = noteRes.message;
        return;
      }
      items.push({ accountId: Number(input.dataset.amount), amount: V.fromMinor(res.value), note: noteRes.value });
    });
    if (firstError) return toast(firstError, true);
    if (!items.length) return toast('请至少填写一个账户的金额', true);

    try {
      const res = await API.createEntries(dateRes.value, items);
      toast(`已保存 ${res.saved} 条录入`);
      document.querySelectorAll('[data-amount]').forEach((i) => { i.value = ''; });
      document.querySelectorAll('[data-note]').forEach((i) => { i.value = ''; });
      await loadAccounts();
      await loadQuick();
      await loadHistory();
      await refreshAnalysis();
    } catch (err) {
      toast(errorOf(err), true);
    }
  }

  async function loadHistory() {
    const from = $('hist-from').value;
    const to = $('hist-to').value;
    const accountId = $('hist-account').value;
    if (from && to) {
      const r = V.range(from, to);
      if (!r.ok) return toast(r.message, true);
    }
    const qs = `?pageSize=200&from=${from}&to=${to}${accountId ? '&accountId=' + accountId : ''}`;
    try {
      const page = await API.listEntries(qs);
      renderHistory(page);
    } catch (err) {
      toast(errorOf(err), true);
    }
  }

  function renderHistory(page) {
    const body = $('hist-body');
    const items = arr(page.items);
    body.innerHTML = '';
    $('hist-empty').classList.toggle('hidden', items.length > 0);
    $('hist-total').textContent = `共 ${page.total || 0} 条${(page.total || 0) > items.length ? `（显示最近 ${items.length} 条）` : ''}`;
    items.forEach((e) => {
      const tr = document.createElement('tr');
      tr.dataset.id = e.id;
      tr.innerHTML = `
        <td>${e.date}</td>
        <td>${escapeHtml(e.accountName)} <span class="tag">${e.currency}</span></td>
        <td class="num">${money(e.amount)}</td>
        <td>${escapeHtml(e.note || '')}</td>
        <td class="ops">
          <button class="link" data-edit-entry="${e.id}">编辑</button>
          <button class="link danger" data-del-entry="${e.id}">删除</button>
        </td>`;
      body.appendChild(tr);
    });
    body.querySelectorAll('[data-edit-entry]').forEach((b) => b.addEventListener('click', () => {
      startEditEntry(items.find((e) => e.id === Number(b.dataset.editEntry)));
    }));
    body.querySelectorAll('[data-del-entry]').forEach((b) => b.addEventListener('click', async () => {
      if (!window.confirm('确认删除这条录入记录？')) return;
      try {
        await API.deleteEntry(Number(b.dataset.delEntry));
        await loadHistory();
        await loadAccounts();
        await refreshAnalysis();
        toast('已删除');
      } catch (err) {
        toast(errorOf(err), true);
      }
    }));
  }

  function startEditEntry(entry) {
    if (state.editingEntryId) return toast('请先完成当前编辑', true);
    const row = document.querySelector(`#hist-body tr[data-id="${entry.id}"]`);
    if (!row) return;
    state.editingEntryId = entry.id;
    row.innerHTML = `
      <td><input type="date" data-f="date" value="${entry.date}" max="${V.todayStr()}" /></td>
      <td>${escapeHtml(entry.accountName)} <span class="tag">${entry.currency}</span></td>
      <td class="num"><input type="text" data-f="amount" value="${entry.amount.toFixed(2)}" /></td>
      <td><input type="text" data-f="note" value="${escapeHtml(entry.note || '')}" maxlength="200" /></td>
      <td class="ops">
        <button class="link" data-save-entry="${entry.id}">保存</button>
        <button class="link" data-cancel-entry>取消</button>
      </td>`;
    row.querySelector('[data-cancel-entry]').addEventListener('click', () => {
      state.editingEntryId = null;
      loadHistory();
    });
    row.querySelector('[data-save-entry]').addEventListener('click', async () => {
      const dateRes = V.date(row.querySelector('[data-f="date"]').value);
      if (!dateRes.ok) return toast(dateRes.message, true);
      const amtRes = V.amount(row.querySelector('[data-f="amount"]').value);
      if (!amtRes.ok) return toast(amtRes.message, true);
      const noteRes = V.note(row.querySelector('[data-f="note"]').value);
      if (!noteRes.ok) return toast(noteRes.message, true);
      try {
        await API.updateEntry(entry.id, {
          date: dateRes.value,
          amount: V.fromMinor(amtRes.value),
          note: noteRes.value,
        });
        state.editingEntryId = null;
        await loadHistory();
        await loadAccounts();
        await refreshAnalysis();
        toast('已更新');
      } catch (err) {
        toast(errorOf(err), true);
      }
    });
  }

  // ---------- 分析 ----------

  function bindAnalysis() {
    $('ana-refresh').addEventListener('click', refreshAnalysis);
    $('ana-series').addEventListener('click', loadSeries);
    $('ana-currency').addEventListener('change', refreshAnalysis);
    document.querySelectorAll('[data-range]').forEach((b) => b.addEventListener('click', () => {
      const today = V.todayStr();
      const map = { '1m': -1, '3m': -3, '1y': -12 };
      $('ana-to').value = today;
      $('ana-from').value = b.dataset.range === 'all' ? '2000-01-01' : shiftMonths(today, map[b.dataset.range]);
      loadSeries();
    }));
  }

  async function refreshAnalysis() {
    try {
      const date = $('ana-date').value || V.todayStr();
      const currency = $('ana-currency').value || 'CNY';
      const res = await API.summary(date, currency);
      const accounts = arr(res.accounts);
      const categories = arr(res.categories);
      $('ana-total').textContent = money(res.total);
      $('ana-date-label').textContent = `${res.currency} · 截至 ${res.date}`;
      $('ana-count').textContent = accounts.length;
      if (categories.length) {
        $('ana-top').textContent = categories[0].label;
        $('ana-top-sub').textContent = `${money(categories[0].amount)} · ${(categories[0].ratio * 100).toFixed(1)}%`;
      } else {
        $('ana-top').textContent = '—';
        $('ana-top-sub').textContent = '';
      }
      C.category(categories);
      C.accounts(accounts.slice(0, 12));
      await loadSeries();
    } catch (err) {
      toast(errorOf(err), true);
    }
  }

  async function loadSeries() {
    const from = $('ana-from').value;
    const to = $('ana-to').value || V.todayStr();
    const currency = $('ana-currency').value || 'CNY';
    if (from && to) {
      const r = V.range(from, to);
      if (!r.ok) return toast(r.message, true);
    }
    try {
      const [series, cmp] = await Promise.all([
        API.timeseries(from, to, currency, 'day'),
        API.compare(from, to, currency),
      ]);
      C.line(arr(series.points));
      $('cmp-start').textContent = money(cmp.start.total);
      $('cmp-start-date').textContent = cmp.start.date;
      $('cmp-end').textContent = money(cmp.end.total);
      $('cmp-end-date').textContent = cmp.end.date;
      const changeEl = $('cmp-change');
      changeEl.textContent = (cmp.change >= 0 ? '+' : '') + money(cmp.change);
      changeEl.className = 'card-value ' + (cmp.change > 0 ? 'up' : cmp.change < 0 ? 'down' : '');
      $('cmp-ratio').textContent = cmp.start.total
        ? `${(cmp.changeRatio * 100).toFixed(2)}%`
        : '期初为 0，无法计算变化率';
    } catch (err) {
      toast(errorOf(err), true);
    }
  }

  // ---------- 数据 ----------

  function bindData() {
    $('pw-form').addEventListener('submit', async (e) => {
      e.preventDefault();
      const oldPw = $('pw-old').value;
      const newPw = $('pw-new').value;
      if (!oldPw) return toast('请输入原密码', true);
      if (newPw.length < 6) return toast('新密码至少需要 6 个字符', true);
      if (newPw !== $('pw-new2').value) return toast('两次输入的新密码不一致', true);
      try {
        const res = await API.changePassword(oldPw, newPw);
        API.setToken(res.token);
        $('pw-old').value = '';
        $('pw-new').value = '';
        $('pw-new2').value = '';
        $('pw-result').textContent = '密码已更新，其他已解锁的窗口需要重新解锁。';
        toast('密码已修改');
      } catch (err) {
        toast(errorOf(err), true);
      }
    });

    $('exp-entries').addEventListener('click', async () => {
      try {
        await API.exportEntries($('exp-from').value, $('exp-to').value);
        toast('录入明细已导出');
      } catch (err) { toast(errorOf(err), true); }
    });
    $('exp-summary').addEventListener('click', async () => {
      try {
        await API.exportSummary($('ana-date').value || V.todayStr());
        toast('汇总已导出');
      } catch (err) { toast(errorOf(err), true); }
    });
    $('exp-series').addEventListener('click', async () => {
      try {
        await API.exportTimeseries($('exp-from').value, $('exp-to').value);
        toast('走势已导出');
      } catch (err) { toast(errorOf(err), true); }
    });

    $('bak-run').addEventListener('click', async () => {
      try {
        const res = await API.backupFile($('bak-dir').value.trim());
        $('bak-result').textContent = `备份成功：${res.path}`;
        toast('数据库已备份');
      } catch (err) {
        $('bak-result').textContent = '';
        toast(errorOf(err), true);
      }
    });

    $('bak-restore').addEventListener('click', async () => {
      const file = $('bak-file').files[0];
      if (!file) return toast('请选择 .db 备份文件', true);
      if (!window.confirm('恢复将覆盖当前全部数据，确认继续？')) return;
      try {
        await API.restoreFile(file.path);
        $('bak-result').textContent = `已从 ${file.path} 恢复`;
        await loadAccounts();
        await loadQuick();
        await loadHistory();
        await refreshAnalysis();
        toast('恢复完成');
      } catch (err) {
        toast(errorOf(err), true);
      }
    });

    $('snap-export').addEventListener('click', async () => {
      try {
        const snap = await API.snapshot();
        const blob = new Blob([JSON.stringify(snap, null, 2)], { type: 'application/json' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `fam-snapshot-${V.todayStr()}.json`;
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
        toast('快照已导出');
      } catch (err) { toast(errorOf(err), true); }
    });

    $('snap-import').addEventListener('click', async () => {
      const file = $('snap-file').files[0];
      if (!file) return toast('请选择快照 JSON 文件', true);
      try {
        const text = await file.text();
        const payload = JSON.parse(text);
        await API.restore($('snap-mode').value, payload);
        $('snap-result').textContent = `已导入 ${payload.accounts ? payload.accounts.length : 0} 个账户`;
        await loadAccounts();
        await loadQuick();
        await loadHistory();
        await refreshAnalysis();
        toast('快照导入完成');
      } catch (err) {
        toast(errorOf(err), true);
      }
    });
  }

  function escapeHtml(s) {
    return String(s === null || s === undefined ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  document.addEventListener('DOMContentLoaded', boot);
})();
