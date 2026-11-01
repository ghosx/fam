/**
 * 脚本公共工具：定位 go 可执行文件、运行子进程。
 */
const { spawnSync } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');

const ROOT = path.join(__dirname, '..');
const SERVER_DIR = path.join(ROOT, 'server');
const WEB_DIR = path.join(ROOT, 'app', 'src');
const BUILD_DIR = path.join(ROOT, 'build');

/** 返回平台对应的后端二进制文件名。 */
function serverBinary() {
  return path.join(BUILD_DIR, process.platform === 'win32' ? 'fam-server.exe' : 'fam-server');
}

/** 定位 go 可执行文件（优先 PATH，其次 scoop 默认安装位置）。 */
function resolveGo() {
  const candidates = [];
  for (const dir of (process.env.PATH || '').split(path.delimiter)) {
    if (!dir) continue;
    candidates.push(path.join(dir, 'go.exe'), path.join(dir, 'go'));
  }
  const home = process.env.USERPROFILE || process.env.HOME || '';
  candidates.push(
    path.join(home, 'scoop', 'apps', 'go', 'current', 'bin', 'go.exe'),
    path.join(home, 'scoop', 'apps', 'go', 'current', 'bin', 'go'),
    path.join(home, 'go', 'bin', 'go.exe'),
    path.join(home, 'go', 'bin', 'go'),
    'C:\\Program Files\\Go\\bin\\go.exe',
    'C:\\Go\\bin\\go.exe',
  );
  for (const c of candidates) {
    if (c && fs.existsSync(c)) return c;
  }
  return null;
}

/** 运行命令，返回是否成功。 */
function run(cmd, args, opts) {
  const res = spawnSync(cmd, args, { stdio: 'inherit', ...opts });
  if (res.error) {
    console.error(`执行失败 ${cmd}: ${res.error.message}`);
    return false;
  }
  return res.status === 0;
}

module.exports = { ROOT, SERVER_DIR, WEB_DIR, BUILD_DIR, serverBinary, resolveGo, run };
