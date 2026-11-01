# fam · 家庭财产管理

<img src="build-resources/icon.png" width="88" align="right" alt="fam" />

一个跑在自己电脑上的家庭财产管理工具：**Electron 桌面前端 + Go 后端 + 纯 Go 编译的 SQLite**。
人工定期记录各账户余额，自动汇总总资产、分布占比与走势，数据**整库加密**保存在本机，不联网、不上传。

[![CI](https://github.com/ghosx/fam/actions/workflows/ci.yml/badge.svg)](https://github.com/ghosx/fam/actions/workflows/ci.yml)
[![Release](https://github.com/ghosx/fam/actions/workflows/release.yml/badge.svg)](https://github.com/ghosx/fam/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/ghosx/fam?color=2f6feb)](https://github.com/ghosx/fam/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11-0078d4)

---

## 为什么做这个

市面上的记账 App 大多要求注册、上传、同步；家庭资产盘点其实只需要**每月花两分钟填几个数字**。
fam 把这个动作做到极简：不记账、不联网、不做自动抓取，只关心「我现在有多少钱、比上个月多了还是少了」。

## 功能

| 模块 | 能力 |
|---|---|
| **账户管理** | 银行 / 支付宝 / 微信 / 股票 / 证券 / 保险 / 现金等类别的**新增、编辑、归档**，记录名称、类别、币种、备注 |
| **定期录入** | 按日期**单条或批量**录入余额/市值（可录负数表示负债），可查看、修改、删除历史记录；同账户同日重复录入自动覆盖 |
| **分析走势** | 总资产、类别与账户分布占比、时间序列走势（未录入的日期沿用上次余额）、任意区间对比（变化额与变化率） |
| **导出备份** | 导出明细/汇总/走势为 CSV（UTF-8 BOM，Excel 直接打开）或 JSON；数据库加密备份与恢复；JSON 快照导入导出 |
| **安全** | 首次启动设置密码，之后每次启动解锁；数据库 **AES-256-GCM 整库加密**，直接读取文件只能看到密文 |

界面为四个页签：**账户 / 录入 / 分析 / 数据**，全部键盘可达，右上角显示服务与加密状态。

## 下载安装

前往 [Releases](https://github.com/ghosx/fam/releases/latest) 下载 `fam-Setup-x.y.z.exe`：

1. 运行安装包，可自选安装目录，自动创建桌面与开始菜单快捷方式
2. **首次启动会要求设置访问密码**（至少 6 位）
3. 之后每次启动输入密码解锁；右上角「锁定」可随时锁定

> 安装包未做代码签名，Windows SmartScreen 可能提示"未知发布者"，选择「仍要运行」即可。

## 安全模型

```
磁盘   fam.db = FAMVAULT 容器（头部 + AES-256-GCM 密文）
                     ▲ 每次写入后整体重加密、原子替换
内存   SQLite 内存库（仅解锁期间存在，锁定 / 退出即丢弃）
```

- **信封加密**：口令 → PBKDF2-HMAC-SHA256（12 万次迭代）→ KEK；随机 32 字节 DEK 加密正文，KEK 只包裹 DEK，改密码无需重写数据
- **未解锁时不加载数据**：内存与磁盘同时为空，接口一律拒绝
- **防篡改**：文件头作为 AAD 参与 GCM 校验，任意字节被改动都会解密失败
- **防暴力**：连续 5 次密码错误锁定 60 秒
- 后端只监听 `127.0.0.1` 随机端口，页面由后端同源提供；除登录相关接口外都要求会话令牌

已知边界：**密码无法找回**（无后门、无恢复码）；运行期间明文存在于进程内存，内存读取类攻击不在防护范围；CSV / JSON 快照是主动导出的明文，请自行保管。

## 技术栈

| 层 | 选型 |
|---|---|
| 前端 | Electron 33 + 原生 JS（无框架）+ Chart.js |
| 后端 | Go，`net/http` 标准库路由 |
| 存储 | SQLite（`modernc.org/sqlite`，纯 Go、零 cgo） |
| 加密 | AES-256-GCM + PBKDF2-HMAC-SHA256，自研加密容器 |
| 打包 | electron-builder（NSIS） |

## 快速开始（开发）

```bash
npm install      # 安装 Electron 与 Chart.js
npm start        # 一键启动：自动检查依赖、编译 Go 后端、拉起 Electron
```

Windows 也可直接双击根目录的 **`start.cmd`**。

启动器会自动完成：依赖检查 → 前端资源复制 → 后端按需编译（源码比二进制新才编）→ 启动 Electron 并注入后端端口。

可选参数：

```bash
npm run start:rebuild   # 强制重新编译后端
npm run start:smoke     # 冒烟模式：自动校验链路后退出（临时数据目录）
start.cmd --smoke       # start.cmd 支持直接传参
```

单独构建：

```bash
npm run build          # 编译后端 + 复制前端依赖
npm run build:server   # 只编译后端
npm run icon           # 重新生成应用图标（纯代码绘制，无需素材）
```

## 测试

```bash
npm test               # 后端 API + 前端校验用例
npm run test:server    # 仅后端（Go）
npm run test:frontend  # 仅前端（node:test）
npm run smoke          # 端到端：真实二进制 + Electron 窗口跑通链路后自动退出
```

覆盖 **121 项后端用例、16 项前端用例、5 项端到端冒烟**，包含：账户增改归档、批量录入与覆盖、走势 carry-forward 口径、区间对比、CSV 导出、备份恢复、密码鉴权与锁定、加密容器往返与篡改检测、旧库迁移、前端输入校验、图表尺寸回归等。用例清单见 [`docs/TESTCASES.md`](docs/TESTCASES.md)。

## 打包

```bash
npm run dist    # → dist/fam-Setup-<version>.exe（NSIS 安装包）
```

`npm run dist` 依次：编译 Go 后端 → 复制前端依赖 → 生成图标 → 调用 electron-builder。

推送 `v*` 标签会触发 [Release workflow](.github/workflows/release.yml)，在 Windows runner 上跑完测试并直接发布带安装包的 Release：

```bash
git tag v1.0.0
git push origin v1.0.0
```

## 数据位置

默认 `%LOCALAPPDATA%\fam\fam.db`（加密容器），可用环境变量覆盖：

| 变量 | 说明 |
|---|---|
| `FAM_HOME` | 数据目录 |
| `FAM_DB` | 数据库文件完整路径 |
| `--db` / `--web` / `--addr` | 后端启动参数 |

卸载程序不会删除数据目录。

## 目录结构

```
app/                  Electron 主进程、预加载脚本、前端页面（app/src）
server/               Go 后端
  internal/model/     领域模型与枚举
  internal/store/     内存 SQLite + 加密持久化 + 统计
  internal/vault/     加密容器（密钥包裹、原子写入、篡改检测）
  internal/api/       HTTP 接口与鉴权
scripts/              build / start / test / smoke / make-icon
docs/                 DESIGN.md（设计）、TESTCASES.md（用例）
tests/frontend/       前端校验测试
.github/workflows/    CI 与 Release
```

## 设计文档

- [`docs/DESIGN.md`](docs/DESIGN.md)：架构、数据模型、统计口径、HTTP API、加密与鉴权方案
- [`docs/TESTCASES.md`](docs/TESTCASES.md)：完整测试用例清单

## 已知限制

- 仅支持 **Windows**（安装包与图标资源针对 Windows 构建，代码本身跨平台）
- 不做汇率折算：多币种按币种分组呈现，避免引入不可靠汇率
- 不做自动抓取、记账流水、预算、多用户与云同步

## 许可

[MIT](LICENSE) © 2026 ghosx
