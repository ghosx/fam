/**
 * 端到端冒烟：启动真实后端二进制 → 访问健康检查 → 拉起 Electron 窗口后自动退出。
 * 用法：node scripts/smoke.js
 */
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawn, spawnSync } = require('node:child_process');
const { ROOT, WEB_DIR, serverBinary, resolveGo, run } = require('./util');

/** 启动后端二进制并返回端口。 */
function launchBackend(dbPath) {
  return new Promise((resolve, reject) => {
    const child = spawn(serverBinary(), ['--web', WEB_DIR, '--db', dbPath], {
      cwd: ROOT,
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true,
    });
    let buf = '';
    const timer = setTimeout(() => reject(new Error('后端启动超时：' + buf.trim())), 30000);
    const onData = (c) => {
      buf += c.toString();
      const m = buf.match(/FAM_READY\s+(\d+)/);
      if (m) {
        clearTimeout(timer);
        resolve({ child, port: Number(m[1]), log: buf });
      }
    };
    child.stdout.on('data', onData);
    child.stderr.on('data', onData);
    child.on('exit', (code) => {
      clearTimeout(timer);
      reject(new Error('后端提前退出，代码 ' + code + '：' + buf.trim()));
    });
  });
}

/** 访问健康检查接口。 */
function health(port) {
  return new Promise((resolve, reject) => {
    const req = http.get(`http://127.0.0.1:${port}/api/health`, (res) => {
      let body = '';
      res.on('data', (c) => { body += c; });
      res.on('end', () => resolve({ status: res.statusCode, body }));
    });
    req.on('error', reject);
    req.setTimeout(5000, () => req.destroy(new Error('健康检查超时')));
  });
}

(async () => {
  const steps = [];
  const record = (name, ok, detail) => {
    steps.push({ name, ok, detail });
    console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ' — ' + detail : ''}`);
  };

  // H01 编译后端
  if (!fs.existsSync(serverBinary())) {
    const go = resolveGo();
    if (!go) {
      record('H01 编译后端', false, '未找到 Go 工具链');
      process.exit(1);
    }
    record('H01 编译后端', run(go, ['build', '-o', serverBinary(), '.'], { cwd: path.join(ROOT, 'server') }));
  } else {
    record('H01 编译后端', true, '已存在 ' + path.basename(serverBinary()));
  }

  // H02 / H03 启动二进制并访问健康检查
  const db = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'fam-smoke-')), 'fam.db');
  let launched;
  try {
    launched = await launchBackend(db);
    record('H02 启动二进制并访问 /api/health', true, '端口 ' + launched.port);
    const res = await health(launched.port);
    record('H02 健康检查返回 200', res.status === 200 && res.body.includes('"ok"'), res.body.trim());
  } catch (err) {
    record('H02 启动二进制并访问 /api/health', false, err.message);
  } finally {
    if (launched) launched.child.kill();
  }

  // H04 Electron 冒烟启动
  let electronBin = null;
  try {
    electronBin = require('electron');
  } catch (e) {
    electronBin = null;
  }
  if (!electronBin) {
    record('H04 Electron 启动', false, '缺少 Electron，请先 npm install');
  } else {
    const env = { ...process.env };
    delete env.ELECTRON_RUN_AS_NODE;
    const res = spawnSync(electronBin, ['.', '--smoke'], { cwd: ROOT, encoding: 'utf8', timeout: 180000, env });
    const log = (res.stdout || '') + (res.stderr || '');
    record('H04 Electron 启动（--smoke 自动退出）', res.status === 0, `退出码 ${res.status}`);
    const line = log.split('\n').find((l) => l.includes('SMOKE '));
    record('H05 前端→后端→数据库链路', Boolean(line && line.includes('SMOKE OK')), line ? line.trim() : '未捕获 SMOKE 输出');
    if (res.status !== 0) console.log(log.slice(-2000));
  }

  const failed = steps.filter((s) => !s.ok);
  console.log(failed.length ? `\n冒烟失败 ${failed.length} 项` : '\n冒烟全部通过');
  process.exit(failed.length ? 1 : 0);
})();
