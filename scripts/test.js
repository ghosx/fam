/**
 * 测试入口：node scripts/test.js [server|frontend]
 */
const fs = require('node:fs');
const path = require('node:path');
const { ROOT, SERVER_DIR, resolveGo, run } = require('./util');

/** 后端测试。 */
function testServer() {
  const go = resolveGo();
  if (!go) {
    console.error('未找到 Go 工具链');
    return false;
  }
  console.log('运行后端测试…');
  return run(go, ['test', './...', '-count=1'], { cwd: SERVER_DIR });
}

/** 前端校验逻辑测试。 */
function testFrontend() {
  const dir = path.join(ROOT, 'tests', 'frontend');
  const files = fs.readdirSync(dir)
    .filter((f) => f.endsWith('.test.js'))
    .map((f) => path.join('tests', 'frontend', f));
  if (!files.length) {
    console.error('未找到前端测试用例');
    return false;
  }
  console.log('运行前端测试…');
  return run(process.execPath, ['--test', ...files], { cwd: ROOT });
}

const target = process.argv[2] || 'all';
let ok = true;
if (target === 'server' || target === 'all') ok = testServer() && ok;
if (target === 'frontend' || target === 'all') ok = testFrontend() && ok;

console.log(ok ? '\n全部测试通过' : '\n存在失败的测试');
process.exit(ok ? 0 : 1);
