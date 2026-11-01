/**
 * 锁屏：首次启动设置密码 / 后续启动解锁。
 */
(function (root) {
  const API = root.FamAPI;
  let onUnlocked = null;

  const $ = (id) => document.getElementById(id);

  function showError(el, msg) {
    el.textContent = msg;
    el.classList.remove('hidden');
  }

  function clearError(el) {
    el.textContent = '';
    el.classList.add('hidden');
  }

  /** 显示锁屏。mode: 'setup' | 'unlock' */
  function show(mode) {
    const screen = $('lock-screen');
    screen.classList.remove('hidden');
    const isSetup = mode === 'setup';
    $('lock-setup').classList.toggle('hidden', !isSetup);
    $('lock-unlock').classList.toggle('hidden', isSetup);
    $('lock-subtitle').textContent = isSetup
      ? '首次使用：设置密码后，本机数据将以该密码加密保存'
      : '输入访问密码以解密并解锁本机数据';
    const input = isSetup ? $('setup-password') : $('unlock-password');
    clearError($('setup-error'));
    clearError($('unlock-error'));
    if (isSetup) {
      $('setup-password').value = '';
      $('setup-password2').value = '';
    } else {
      $('unlock-password').value = '';
    }
    if (input) {
      input.focus();
      setTimeout(() => input.focus(), 60);
    }
  }

  function hide() {
    $('lock-screen').classList.add('hidden');
  }

  /** 校验设置密码的输入。 */
  function checkSetup(pw, pw2) {
    const res = root.FamValidate.passwordPair(pw, pw2);
    return res.ok ? '' : res.message;
  }

  async function submitSetup(e) {
    e.preventDefault();
    const errEl = $('setup-error');
    const pw = $('setup-password').value;
    const pw2 = $('setup-password2').value;
    const msg = checkSetup(pw, pw2);
    if (msg) return showError(errEl, msg);
    clearError(errEl);
    $('setup-submit').disabled = true;
    try {
      const res = await API.setupPassword(pw);
      API.setToken(res.token);
      hide();
      if (onUnlocked) await onUnlocked();
    } catch (err) {
      showError(errEl, err.message || '设置失败');
    } finally {
      $('setup-submit').disabled = false;
    }
  }

  async function submitUnlock(e) {
    e.preventDefault();
    const errEl = $('unlock-error');
    const pw = $('unlock-password').value;
    if (!pw) return showError(errEl, '请输入密码');
    clearError(errEl);
    $('unlock-submit').disabled = true;
    try {
      const res = await API.login(pw);
      API.setToken(res.token);
      hide();
      if (onUnlocked) await onUnlocked();
    } catch (err) {
      showError(errEl, err.message || '解锁失败');
    } finally {
      $('unlock-submit').disabled = false;
    }
  }

  /** 锁定当前窗口：注销会话并回到解锁界面。 */
  async function lockNow() {
    try {
      await API.logout();
    } catch (e) {
      /* 忽略：令牌可能已失效 */
    }
    API.setToken('');
    show('unlock');
  }

  /** 绑定表单事件。 */
  function init(opts) {
    onUnlocked = opts && opts.onUnlocked;
    $('lock-setup').addEventListener('submit', submitSetup);
    $('lock-unlock').addEventListener('submit', submitUnlock);
    API.onUnauthorized(() => {
      API.setToken('');
      show('unlock');
    });
  }

  root.FamLock = { init, show, hide, lockNow };
})(typeof window !== 'undefined' ? window : this);
