/**
 * Electron 主进程：启动本机 Go 后端，打开前端窗口，退出时关闭后端。
 * 开发态从 build/ 读后端二进制；打包后从 resources/ 读取。
 */
const { app, BrowserWindow, dialog, ipcMain } = require('electron');
const { spawn } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');

const ROOT = path.join(__dirname, '..');
const PACKAGED = app.isPackaged;
const WEB_DIR = PACKAGED ? path.join(process.resourcesPath, 'web') : path.join(ROOT, 'app', 'src');
const SERVER_BIN = PACKAGED
  ? path.join(process.resourcesPath, process.platform === 'win32' ? 'fam-server.exe' : 'fam-server')
  : path.join(ROOT, 'build', process.platform === 'win32' ? 'fam-server.exe' : 'fam-server');
const APP_ICON = PACKAGED
  ? path.join(process.resourcesPath, 'icon.ico')
  : path.join(ROOT, 'build-resources', 'icon.ico');

let backend = null;
let backendPort = 0;
let mainWindow = null;
let smokeMode = false;

/** 启动后端进程并解析实际端口。 */
function startBackend() {
  const env = { ...process.env };
  if (process.argv.includes('--smoke')) {
    // 冒烟模式使用临时数据目录，避免污染真实数据
    env.FAM_HOME = path.join(app.getPath('temp'), `fam-smoke-${Date.now()}`);
  }

  return new Promise((resolve, reject) => {
    let command;
    let args;
    if (fs.existsSync(SERVER_BIN)) {
      command = SERVER_BIN;
      args = ['--web', WEB_DIR];
    } else if (!PACKAGED) {
      command = 'go';
      args = ['run', './server', '--web', WEB_DIR];
    } else {
      reject(new Error('安装包缺少后端程序：' + SERVER_BIN));
      return;
    }

    const child = spawn(command, args, {
      cwd: PACKAGED ? path.dirname(SERVER_BIN) : ROOT,
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
      env,
    });
    backend = child;

    let buf = '';
    const timer = setTimeout(() => {
      reject(new Error('后端启动超时（60 秒）：' + buf.trim()));
    }, 60000);

    const onData = (chunk) => {
      buf += chunk.toString();
      const m = buf.match(/FAM_READY\s+(\d+)/);
      if (m) {
        clearTimeout(timer);
        backendPort = Number(m[1]);
        resolve(backendPort);
      }
    };
    child.stdout.on('data', onData);
    child.stderr.on('data', onData);
    child.on('error', (err) => {
      clearTimeout(timer);
      reject(new Error('无法启动后端（' + command + '）：' + err.message));
    });
    child.on('exit', (code) => {
      clearTimeout(timer);
      if (!backendPort) reject(new Error('后端进程提前退出，代码 ' + code + '：' + buf.trim()));
    });
  });
}

/** 终止后端进程（含子进程），并等待其退出。 */
function stopBackend() {
  return new Promise((resolve) => {
    if (!backend || backend.exitCode !== null) {
      backend = null;
      resolve();
      return;
    }
    const child = backend;
    const pid = child.pid;
    const done = () => {
      backend = null;
      resolve();
    };
    const timer = setTimeout(done, 3000);
    child.once('exit', () => {
      clearTimeout(timer);
      done();
    });
    try {
      if (process.platform === 'win32') {
        spawn('taskkill', ['/pid', String(pid), '/t', '/f'], { stdio: 'ignore', windowsHide: true });
      } else {
        process.kill(-pid, 'SIGTERM');
      }
    } catch (e) {
      clearTimeout(timer);
      done();
    }
  });
}

/** 冒烟模式收尾：先关窗口让渲染子进程退出，再结束后端，最后按结果退出。 */
async function finishSmoke(code, detail) {
  smokeMode = true;
  console.log(code === 0 ? 'SMOKE OK ' + detail : 'SMOKE FAILED ' + detail);
  if (mainWindow && !mainWindow.isDestroyed()) mainWindow.destroy();
  await stopBackend();
  app.exit(code);
}

function createWindow() {
  const win = new BrowserWindow({
    width: 1180,
    height: 780,
    minWidth: 900,
    minHeight: 600,
    title: 'fam · 家庭财产管理',
    icon: fs.existsSync(APP_ICON) ? APP_ICON : undefined,
    show: false,
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  win.setMenuBarVisibility(false);
  win.once('ready-to-show', () => win.show());
  win.loadURL(`http://127.0.0.1:${backendPort}/`);
  return win;
}

/** 单实例：避免重复启动多个后端与窗口。 */
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore();
      mainWindow.focus();
    }
  });

  app.on('before-quit', stopBackend);
  app.on('window-all-closed', () => {
    if (smokeMode) return; // 冒烟模式由 finishSmoke 控制退出码
    stopBackend();
    app.quit();
  });

  app.whenReady().then(async () => {
    try {
      await startBackend();
    } catch (err) {
      dialog.showErrorBox('fam 启动失败', String(err && err.message ? err.message : err));
      app.quit();
      return;
    }
    mainWindow = createWindow();
    if (!process.argv.includes('--smoke')) return;

    // 冒烟模式：页面加载后执行一次真实的「首次设密码 → 解锁 → 增删改查 → 图表」链路校验
    mainWindow.webContents.on('console-message', (event, level, message) => {
      if (level >= 2) console.error('[页面错误]', message);
    });
    mainWindow.webContents.once('did-fail-load', () => {
      finishSmoke(1, '页面加载失败');
    });
    mainWindow.webContents.once('did-finish-load', async () => {
      await new Promise((r) => setTimeout(r, 1200));
      let ok = false;
      let detail = '';
      try {
        const result = await mainWindow.webContents.executeJavaScript(`(async () => {
        const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
        const name = '冒烟账户' + Date.now();

        // 0) 首次启动必须设置访问密码：此时数据接口应拒绝未认证访问
        const gateStatus = await fetch('/api/accounts').then((r) => r.status).catch(() => 0);
        const setupVisible = !document.getElementById('lock-setup').classList.contains('hidden');
        document.getElementById('setup-password').value = 'smoke123456';
        document.getElementById('setup-password2').value = 'smoke123456';
        document.getElementById('lock-setup').dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }));
        await sleep(1400);
        const unlocked = document.getElementById('lock-screen').classList.contains('hidden');

        // 1) 通过界面新增账户（走表单校验 + 渲染）
        document.getElementById('acct-new').click();
        const f = document.getElementById('acct-form');
        f.name.value = name;
        f.category.value = 'bank';
        f.currency.value = 'CNY';
        f.note.value = 'smoke';
        f.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }));
        await sleep(900);
        const rows = document.querySelectorAll('#acct-body tr').length;
        const rowText = document.querySelector('#acct-body tr') ? document.querySelector('#acct-body tr').textContent : '';

        // 2) 通过界面批量录入金额（走快捷录入 + 汇总刷新）
        const amountInput = document.querySelector('#quick-body [data-amount]');
        if (!amountInput) return { gateStatus, setupVisible, unlocked, rows, rowText, histRows: 0, total: '', status: '' };
        amountInput.value = '1234.56';
        document.getElementById('entry-save').click();
        await sleep(1500);

        const histRows = document.querySelectorAll('#hist-body tr').length;
        const total = document.getElementById('ana-total').textContent;

        // 3) 切到分析页，校验图表容器尺寸稳定（回归：canvas 与容器互相撑高会导致空白/超高）
        document.querySelector('[data-tab="analysis"]').click();
        let chartBoxH = 0;
        let chartCanvasH = 0;
        for (let i = 0; i < 20; i++) {
          await sleep(200);
          if (window.FamCharts) window.FamCharts.resizeAll();
          chartBoxH = Math.round(document.querySelector('.charts .chart-box:nth-child(2)').getBoundingClientRect().height);
          chartCanvasH = Math.round(document.querySelector('#chart-account').getBoundingClientRect().height);
          if (chartCanvasH > 0) break;
        }
        const chartRendered = typeof window.Chart !== 'undefined' && !!window.Chart.getChart('chart-account');

        return {
          gateStatus,
          setupVisible,
          unlocked,
          rows,
          rowText,
          histRows,
          total,
          chartBoxH,
          chartCanvasH,
          chartRendered,
          status: document.getElementById('status').textContent,
        };
      })()`);
        ok = result.gateStatus === 401 && result.setupVisible === true && result.unlocked === true &&
          result.rows >= 1 && result.rowText.includes('冒烟') && result.histRows >= 1 &&
          result.total.replace(/[\\s,]/g, '').includes('1234.56') && result.status.includes('服务正常') &&
          result.chartRendered === true && result.chartCanvasH >= 120 &&
          result.chartCanvasH <= result.chartBoxH && result.chartBoxH <= 400;
        detail = JSON.stringify(result);

        // 4) 刷新页面后应保持解锁状态，且不会出现「空白锁屏」（回归：Ctrl+R 后没有密码输入框）
        try {
          await mainWindow.webContents.executeJavaScript('setTimeout(() => location.reload(), 0); 1');
        } catch (e) {
          /* 导航会销毁执行上下文，忽略 */
        }
        await new Promise((r) => setTimeout(r, 2200));
        const reloadState = await mainWindow.webContents.executeJavaScript(`JSON.stringify({
          lockHidden: document.getElementById('lock-screen').classList.contains('hidden'),
          rows: document.querySelectorAll('#acct-body tr').length,
          status: document.getElementById('status').textContent,
        })`);
        const parsed = JSON.parse(reloadState);
        ok = ok && parsed.lockHidden === true && parsed.rows >= 1;
        detail += ' | reload=' + reloadState;
      } catch (err) {
        detail = String(err && err.message ? err.message : err);
      }
      await finishSmoke(ok ? 0 : 1, detail);
    });
  });

  ipcMain.handle('fam:port', () => backendPort);
}
