# fam — 家庭财产管理 · 功能设计

## 1. 目标与边界

本机运行的家庭财产管理工具：人工定期录入各账户余额/市值，汇总总资产、看分布与走势，导出与备份数据。

**纳入范围**：账户管理、定期人工录入、统计与走势、导出与备份、一键启动。
**明确不做**：自动/联网同步抓取、记账流水（收入支出）、预算、多用户与权限、云同步、汇率折算（见 5.3）。

## 2. 总体架构

```
┌──────────────────── Electron 主进程 (app/main.js) ────────────────────┐
│  启动 → spawn Go 后端二进制 → 读取 stdout 中的端口 → 创建 BrowserWindow │
│  渲染进程 (app/src/*) ── fetch http://127.0.0.1:<port>/api/... ─────┐  │
│  退出 → 关闭后端子进程                                              │  │
└─────────────────────────────────────────────────────────────────────┼──┘
                                                                      ▼
┌──────────────────── Go 后端 (server/) ─────────────────────────────────┐
│  net/http 路由 /api/*  +  本机静态资源服务                              │
│  store 层：SQLite（modernc.org/sqlite，纯 Go、无 cgo）                 │
│  数据文件：%LOCALAPPDATA%/fam/fam.db（可用 FAM_DB 覆盖）               │
└────────────────────────────────────────────────────────────────────────┘
```

- 前后端同机、仅监听 `127.0.0.1`，端口由系统分配（`127.0.0.1:0`），后端把 `FAM_READY <port>` 打印到 stdout，Electron 解析后注入渲染进程，避免端口冲突与手工配置。
- 后端同时提供静态文件服务（`GET /` → `app/src/index.html`），便于无 Electron 时用浏览器直接访问调试；Electron 正常路径下使用 `file://` 加载页面。

## 3. 目录结构

```
fam/
├─ package.json              # 一键启动 / 构建 / 测试入口
├─ README.md
├─ docs/DESIGN.md, docs/TESTCASES.md
├─ server/                   # Go 后端
│  ├─ main.go                # 端口分配、路由装配、静态服务、优雅退出
│  └─ internal/
│     ├─ model/              # 数据结构与校验常量
│     ├─ store/              # SQLite 打开、迁移、账户/录入/统计/备份实现
│     └─ api/                # HTTP handler、请求校验、错误响应
├─ app/                      # Electron 前端
│  ├─ main.js, preload.js
│  └─ src/                   # index.html / styles.css / api.js / validate.js / charts.js / app.js
├─ scripts/                  # build.ps1、test.ps1、smoke.ps1
└─ tests/frontend/           # 前端校验逻辑的 node:test 用例
```

## 4. 数据模型

### accounts（财产账户）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | INTEGER PK | |
| name | TEXT NOT NULL | 账户名称，同币种下唯一 |
| category | TEXT NOT NULL | `bank` `alipay` `wechat` `stock` `securities` `insurance` `cash` `other` |
| currency | TEXT NOT NULL | `CNY` `USD` `HKD` `EUR` `JPY` |
| note | TEXT | 备注 |
| archived | INTEGER | 0 有效 / 1 已归档 |
| archived_at | TEXT | 归档时间（YYYY-MM-DD），用于历史走势回溯 |
| created_at / updated_at | TEXT | |

唯一约束：`UNIQUE(name, currency)`。

### balance_entries（人工录入记录）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | INTEGER PK | |
| account_id | INTEGER NOT NULL | 外键 → accounts(id) |
| entry_date | TEXT NOT NULL | YYYY-MM-DD |
| amount_minor | INTEGER NOT NULL | 金额 × 100（分），避免浮点误差；可为负（负债/信用卡） |
| note | TEXT | |

唯一约束：`UNIQUE(account_id, entry_date)` → 同一账户同一天重复录入按**更新**处理（upsert）。

### meta

`key/value` 表，存 `schema_version`。

### 金额约定

- API 与界面使用十进制金额（如 `1234.56`），服务端四舍五入到分后以整数存储，查询时还原为字符串/数值，保证汇总精确。
- 允许负数：信用卡欠款、贷款等以负值录入。

## 5. 统计分析口径

### 5.1 总资产

总资产 = Σ(各有效账户截至该日期最近一次录入的余额)，按币种分别汇总。

### 5.2 时间序列（carry-forward）

对区间内每个日期 D，每个账户取 `entry_date <= D` 的最近一条记录作为该账户在 D 的余额；缺失则为 0。
这样未录入某天的账户会沿用上次余额，走势连续、真实。支持 `groupBy=day|month`（月取当月最后一天口径）。

### 5.3 多币种

不做汇率折算：总资产、占比、走势均按币种分组呈现，界面提供币种切换。避免引入不可靠的汇率源与虚假精度。

### 5.4 归档账户

- 不出现在「快捷录入」与账户默认列表；
- 当前总资产/占比只统计未归档账户；
- 时间序列中，若 `D >= archived_at` 则排除该账户，`D < archived_at` 仍计入，保证历史走势不断裂。

## 6. HTTP API（全部 `/api` 前缀，JSON）

统一错误响应：`{"error":{"code":"BAD_REQUEST","message":"..."}}`。

### 系统
- `GET /api/health` → `{status:"ok", version, dbPath, time}`
- `GET /api/meta/categories` → 类别与币种枚举（前端下拉框数据源）

### 鉴权
- `GET /api/auth/status` → `{initialized, authenticated, passwordUpdatedAt}`（公开）
- `POST /api/auth/setup` `{password}` → 201 `{token}`；已初始化返回 409（公开）
- `POST /api/auth/login` `{password}` → `{token}`；密码错误 401，连续失败 5 次锁定 60 秒（429，公开）
- `POST /api/auth/logout` → 注销当前令牌
- `POST /api/auth/password` `{oldPassword, newPassword}` → 校验旧密码，改密后**所有旧令牌失效**并返回新令牌

除 `health`、`auth/status`、`auth/setup`、`auth/login` 外，所有 `/api/*` 都要求请求头 `Authorization: Bearer <token>`，否则返回 401。

### 账户
- `GET /api/accounts?includeArchived=false&keyword=` → 列表项含 `latestAmount`、`latestDate`
- `POST /api/accounts` `{name, category, currency, note}`
- `PUT /api/accounts/:id` `{name?, category?, currency?, note?}`
- `POST /api/accounts/:id/archive` `{archived:true|false}`

### 录入
- `GET /api/entries?from=&to=&accountId=&page=&pageSize=` → 历史录入记录（含账户名/类别/币种），按日期倒序
- `POST /api/entries` `{date, items:[{accountId, amount, note}]}` → 单条（items 长度 1）与批量共用；按 (账户, 日期) upsert
- `PUT /api/entries/:id` `{date?, amount?, accountId?, note?}`
- `DELETE /api/entries/:id`

### 统计
- `GET /api/stats/summary?date=&currency=` → `{date, currency, total, accounts:[{id,name,category,amount,ratio}], categories:[{category,amount,ratio}]}`
- `GET /api/stats/timeseries?from=&to=&currency=&groupBy=` → `{points:[{date,total,byCategory: {...}}]}`
- `GET /api/stats/compare?from=&to=&currency=` → `{start:{date,total}, end:{date,total}, change, changeRatio}`

### 导出（通用格式）
- `GET /api/export/entries?from=&to=&format=csv|json`
- `GET /api/export/summary?date=&format=`
- `GET /api/export/timeseries?from=&to=&format=`
- CSV 为 UTF-8 带 BOM（Excel 直接打开不乱码），金额两位小数。

### 备份与恢复
- `GET /api/backup/snapshot` → 全量 JSON 快照 `{version, exportedAt, accounts:[...], entries:[...]}`
- `POST /api/backup/restore` `{mode:"replace"|"merge", payload:{...}}`
  - replace：清空后导入；merge：按 `(账户名, 币种)` 定位账户、按 `(账户, 日期)` upsert 录入
- `POST /api/backup/file` `{dir}` → 用 `VACUUM INTO` 生成 `fam-YYYYMMDD-HHMMSS.db`
- `POST /api/backup/file/restore` `{path}` → 校验为合法 fam 库后替换当前库并重新打开（先备份现有文件为 `.bak`）

## 7. 前端界面（单页 + 四个标签页）

1. **账户**：表格（名称/类别/币种/备注/最新余额/最新日期/状态）+ 新增/编辑弹窗 + 归档/恢复 + 关键字筛选；归档行灰显。
2. **录入**：顶部日期选择器 + 账户快速录入表（每账户一行，留空跳过）→ 「批量保存」；下方历史记录表（日期区间/账户筛选），行内编辑与删除。
3. **分析**：币种切换；总资产卡片；类别占比环形图；账户排名条形图；时间走势折线图（起止日期 + 快捷区间 近1月/近3月/近1年/全部）；区间对比卡片（起止总额、变化额、变化率）。
4. **数据**：导出 CSV（明细/汇总/走势）；备份数据库到目录；从 `.db` 文件恢复；导出 JSON 快照 / 导入快照（replace/merge）。

交互约定：所有提交按钮做前端校验（必填、金额格式、日期格式与不可为未来、起止日期顺序），失败在表单处红字提示，不弹窗；后端返回错误统一在顶部 toast 展示。

图表约定：`.chart-box` 使用**固定高度**，canvas 放入 `.chart-holder`（`position: relative` + `min-height: 0`）中。Chart.js 的 `maintainAspectRatio: false` 会按父容器高度设置 canvas，若父容器高度由 canvas 撑开就会形成尺寸正反馈（canvas 被不断撑高，图表画在超大画布中央而显示为空白）。账户分布条形图对超过 10 字的名称做省略号截断，悬停 tooltip 显示完整名称，负值（负债）用红色区分，金额轴用「万/亿」压缩刻度。

## 8. 一键启动

### 入口

- Windows：双击根目录 **`start.cmd`**
- 命令行：`npm start`
- 参数：`--rebuild` 强制重新编译后端、`--smoke` 冒烟模式（临时数据目录，链路校验后自动退出）

### `scripts/start.js` 自举流程

1. **依赖检查**：`node_modules` 缺少 Electron / Chart.js 时自动执行 `npm install`；
2. **前端资源**：把 `chart.js` 复制到 `app/src/vendor/chart.umd.js`（目标缺失或源更新时）；
3. **后端编译**：`build/fam-server(.exe)` 不存在或**比 `server/` 下源码旧**时自动 `go build`，`--rebuild` 可强制；
4. **启动 Electron**（清除 `ELECTRON_RUN_AS_NODE`，避免 Electron 退化为 Node 进程）。

### Electron 主进程（`app/main.js`）

1. 执行 `build/fam-server.exe`（Windows）/ `build/fam-server`，不存在则回退 `go run ./server`；
2. 解析 stdout 的 `FAM_READY <port>`（60 秒超时则弹错误框并退出）；
3. `loadURL http://127.0.0.1:<port>/`：页面由后端同源提供，接口无跨域问题（preload 将 `__FAM_API_BASE__` 置为空串）；
4. 所有窗口关闭 / `before-quit` 时用 `taskkill /t` 结束后端子进程；
5. `--smoke`：以临时 `FAM_HOME` 启动，页面加载后驱动一次「界面新增账户 → 快捷录入 → 汇总」链路，输出 `SMOKE OK` 后自动退出。

数据目录：`%LOCALAPPDATA%\fam`（可用环境变量 `FAM_HOME` 覆盖），首次运行自动创建并执行迁移。

## 9. 加密存储与访问控制

**目标**：本机数据文件即使用文本编辑器/十六进制工具直接打开也看不到任何账户名与金额；密码是唯一的解密入口。

**存储模型**

```
磁盘：fam.db = 加密容器（FAMVAULT 头 + AES-256-GCM 密文）
                 ▲ 每次写入后整体重写（原子替换）
内存：SQLite 内存库（解锁期间存在，锁定/退出即丢弃）
```

- 数据**只在内存中**以 SQLite 形式存在；落盘前导出为结构化镜像再整块加密，磁盘上任何时刻都没有明文
- 未解锁时后端连数据都不加载，内存中同样没有明文
- 容器格式：`magic(8) | version | kdf | iters | salt(16) | kekNonce(12) | wrappedDEK(48) | bodyNonce(12) | 密文正文`
- 密钥体系（信封加密）：口令 → PBKDF2-HMAC-SHA256(120000) → KEK；随机 32 字节 DEK 加密正文，KEK 只包裹 DEK。改密码只需重新包裹 DEK（实现上会整体重写一次）
- 头部作为 AAD 参与 GCM 校验：篡改头部或正文都会导致解密失败并报「文件已损坏或被篡改」
- 原子写入：先写 `fam.db.tmp` 再 `rename`，避免断电产生半截文件

**为什么不用 SQLCipher**：项目要求纯 Go 零 cgo，SQLCipher 需要 cgo；`modernc.org/sqlite` 也没有公开的 VFS 注册接口。其 `Serialize/Deserialize` 在内存库上存在分配器不匹配缺陷（新版已移除），因此采用「内存库 + 结构化镜像 + 整块加密」方案，安全性等价（全密文），且不依赖有缺陷的 API。

**访问控制**

| 项 | 方案 |
|---|---|
| 会话 | 32 字节随机令牌，服务端内存保存，有效期 12 小时 |
| 传输 | 请求头 `Authorization: Bearer <token>` |
| 鉴权门 | 除 `health`/`auth/status`/`auth/setup`/`auth/login` 外，要求「已初始化 + 已解锁 + 令牌有效」 |
| 防暴力 | 同一客户端连续 5 次密码错误锁定 60 秒（429 + `Retry-After`） |
| 锁定 | 「锁定」按钮 = 注销会话 + 清空内存库；进程重启同样回到锁定态 |
| 改密 | 校验旧密码 → 新盐派生 KEK → 重新包裹 DEK → 所有会话失效 |
| 密码规则 | 6–72 字符，不能全为空格 |

**启动流程**：页面加载 → `GET /api/auth/status` → 未初始化则「设置密码」（同时生成密钥）→ 已初始化且未解锁则「解锁」→ 解锁后数据才载入内存。

**备份与导出**：数据库备份是加密容器的副本，必须用同一密码才能恢复；JSON 快照/CSV 是用户主动导出的明文，需自行妥善保管。

**旧版迁移**：首次运行时若检测到旧版明文 `fam.db`，会先只读导入内存，设置（或校验）密码后写出加密容器并清理明文残留。

**已知边界**：密码无法找回，忘记即无法解密数据（无后门、无恢复码）；运行期间明文存在于进程内存中，具备本机内存读取权限的攻击者不在防护范围内。

## 10. 打包与安装（Windows）

```
npm run dist     # → dist/fam-Setup-1.0.0.exe（NSIS 安装包）
```

- 打包工具：electron-builder（NSIS 目标，x64）
- `app/main.js`、`app/preload.js` 打进 `app.asar`；前端页面与 Go 后端二进制作为 `extraResources` 放到 `resources/`，便于 Go 进程以普通文件方式读取
- 安装器：非一键式，**可选择安装目录**、创建桌面与开始菜单快捷方式、注册卸载项
- 安装目录（默认 `%LOCALAPPDATA%\Programs\fam`）与数据目录（`%LOCALAPPDATA%\fam`）分离，卸载不删除用户数据
- 打包后主进程从 `process.resourcesPath` 读取后端与页面，不再依赖 `go` 工具链
- 应用图标由 `scripts/make-icon.js` 纯代码生成（无需外部素材）：蓝色渐变圆角方块 + 白色房屋与递增柱状，输出 16/24/32/48/64/128/256 七个尺寸；16–64 采用 32 位 BMP 条目（兼容性最好），128/256 采用 PNG 条目。同一 ICO 用于 exe 资源、NSIS 安装包与 `BrowserWindow` 窗口图标（开发模式亦生效）

## 11. 校验规则

| 项 | 规则 |
|---|---|
| 账户名称 | 必填，1–50 字符，同币种下不重复 |
| 类别 / 币种 | 必须属于枚举 |
| 备注 | ≤ 200 字符 |
| 录入日期 | `YYYY-MM-DD`，合法日期，不得晚于今天 |
| 金额 | 必填，数字（可负），最多两位小数，绝对值 ≤ 1e12 |
| 日期区间 | from ≤ to |
| 备份恢复文件 | 必须存在且为合法 SQLite 库且含 fam 表结构 |
| 访问密码 | 6–72 字符，不能全为空格；设置时需二次确认 |
