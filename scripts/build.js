/**
 * 构建：编译 Go 后端二进制 + 复制前端依赖到 app/src/vendor。
 * 用法：node scripts/build.js [server|web]
 */
const fs = require('node:fs');
const path = require('node:path');
const { ROOT, SERVER_DIR, WEB_DIR, BUILD_DIR, serverBinary, resolveGo, run } = require('./util');

/** 编译后端。 */
function buildServer() {
  const go = resolveGo();
  if (!go) {
    console.error('未找到 Go 工具链，请先安装 Go 并确保在 PATH 中');
    return false;
  }
  fs.mkdirSync(BUILD_DIR, { recursive: true });
  const out = serverBinary();
  console.log(`编译后端 → ${out}`);
  return run(go, ['build', '-trimpath', '-o', out, '.'], { cwd: SERVER_DIR });
}

/** 复制 Chart.js 到前端 vendor 目录。 */
function buildWeb() {
  const src = path.join(ROOT, 'node_modules', 'chart.js', 'dist', 'chart.umd.js');
  if (!fs.existsSync(src)) {
    console.error('缺少 node_modules/chart.js，请先执行 npm install');
    return false;
  }
  const vendor = path.join(WEB_DIR, 'vendor');
  fs.mkdirSync(vendor, { recursive: true });
  fs.copyFileSync(src, path.join(vendor, 'chart.umd.js'));
  console.log(`复制前端依赖 → ${path.join(vendor, 'chart.umd.js')}`);
  return true;
}

const target = process.argv[2] || 'all';
let ok = true;
if (target === 'server' || target === 'all') ok = buildServer() && ok;
if (target === 'web' || target === 'all') ok = buildWeb() && ok;
process.exit(ok ? 0 : 1);
