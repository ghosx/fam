/**
 * 一键启动：检查依赖 → 按需编译后端 → 复制前端资源 → 拉起 Electron（Electron 会自带起 Go 后端）。
 *
 * 用法：
 *   npm start               正常启动
 *   npm start -- --smoke    冒烟模式：自动校验链路后退出
 *   npm start -- --rebuild  强制重新编译后端
 *   或直接双击根目录的 start.cmd
 */
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { ROOT, SERVER_DIR, WEB_DIR, BUILD_DIR, serverBinary, resolveGo, run } = require('./util');

const argv = process.argv.slice(2);
const forceRebuild = argv.includes('--rebuild');
const passthrough = argv.filter((a) => a !== '--rebuild');
const TOTAL = 3;

function step(n, message) {
  console.log(`\n[${n}/${TOTAL}] ${message}`);
}

function info(message) {
  console.log(`        ${message}`);
}

/** 通过 shell 调用 npm（Windows 下 npm 是 .cmd，必须经 shell 启动）。 */
function npmInstall() {
  const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
  return run(npm, ['install'], { cwd: ROOT, shell: true });
}

/** 1. 确保前端依赖已安装。 */
function ensureDependencies() {
  step(1, '检查前端依赖（Electron / Chart.js）');
  const hasElectron = fs.existsSync(path.join(ROOT, 'node_modules', 'electron'));
  const hasChart = fs.existsSync(path.join(ROOT, 'node_modules', 'chart.js'));
  if (hasElectron && hasChart) {
    info('依赖已就绪');
    return true;
  }
  info('缺少依赖，正在执行 npm install（首次运行需要几分钟）…');
  if (!npmInstall()) {
    console.error('依赖安装失败，请检查网络后重试：npm install');
    return false;
  }
  return true;
}

/** 2. 确保前端静态资源完整（Chart.js 复制到 app/src/vendor）。 */
function ensureWebAssets() {
  const src = path.join(ROOT, 'node_modules', 'chart.js', 'dist', 'chart.umd.js');
  const dst = path.join(WEB_DIR, 'vendor', 'chart.umd.js');
  if (fs.existsSync(dst) && fs.existsSync(src) &&
    fs.statSync(dst).mtimeMs >= fs.statSync(src).mtimeMs) {
    return true;
  }
  try {
    fs.mkdirSync(path.dirname(dst), { recursive: true });
    fs.copyFileSync(src, dst);
    info('已复制前端依赖 chart.umd.js');
    return true;
  } catch (err) {
    console.error('复制前端依赖失败：' + err.message);
    return false;
  }
}

/** 返回目录下最新的文件修改时间。 */
function newestMtime(dir) {
  let newest = 0;
  const walk = (d) => {
    for (const entry of fs.readdirSync(d, { withFileTypes: true })) {
      const full = path.join(d, entry.name);
      if (entry.isDirectory()) walk(full);
      else newest = Math.max(newest, fs.statSync(full).mtimeMs);
    }
  };
  walk(dir);
  return newest;
}

/** 后端源码是否比已编译的二进制更新。 */
function serverOutdated(bin) {
  if (!fs.existsSync(bin)) return true;
  return newestMtime(SERVER_DIR) > fs.statSync(bin).mtimeMs;
}

/** 3. 确保后端二进制是最新的。 */
function ensureServer() {
  step(2, '检查 Go 后端');
  const bin = serverBinary();
  if (!forceRebuild && !serverOutdated(bin)) {
    info('后端程序已是最新：' + path.basename(bin));
    return true;
  }
  const go = resolveGo();
  if (!go) {
    console.error('未找到 Go 工具链。请安装 Go 1.21+ 并加入 PATH，');
    console.error('或从 https://go.dev/dl/ 下载安装后重试。');
    return false;
  }
  info('编译后端 → ' + bin);
  fs.mkdirSync(BUILD_DIR, { recursive: true });
  if (!run(go, ['build', '-trimpath', '-o', bin, '.'], { cwd: SERVER_DIR })) {
    console.error('后端编译失败，请检查 server 目录下的 Go 代码。');
    return false;
  }
  return true;
}

/** 数据目录提示。 */
function printDataLocation() {
  const home = process.env.FAM_HOME || path.join(process.env.LOCALAPPDATA || process.env.HOME || '.', 'fam');
  info('数据文件：' + (process.env.FAM_DB || path.join(home, 'fam.db')));
}

/** 4. 拉起 Electron。 */
function launchElectron() {
  step(3, '启动应用（关闭窗口即退出，后端会一并关闭）');
  let electronBin;
  try {
    electronBin = require('electron');
  } catch (err) {
    console.error('无法加载 Electron，请确认依赖安装完整（npm install）。');
    return 1;
  }
  // 清除 ELECTRON_RUN_AS_NODE，否则 Electron 会退化成普通 Node 进程而无法创建窗口
  const env = { ...process.env };
  delete env.ELECTRON_RUN_AS_NODE;
  const res = spawnSync(electronBin, ['.', ...passthrough], { stdio: 'inherit', cwd: ROOT, env });
  if (res.error) {
    console.error('启动 Electron 失败：' + res.error.message);
    return 1;
  }
  return res.status === null ? 1 : res.status;
}

function main() {
  console.log('fam · 家庭财产管理');
  console.log('=========================================');
  printDataLocation();

  if (!ensureDependencies()) process.exit(1);
  if (!ensureWebAssets()) process.exit(1);
  if (!ensureServer()) process.exit(1);

  const code = launchElectron();
  if (code !== 0) console.error('\n应用异常退出，代码 ' + code);
  process.exit(code);
}

main();
