# AstralOps

[Telegram 群](https://t.me/Project_AstralOps)

**把你的 AI 编程助手带到任何远程服务器。**

AstralOps 是一个专为 Claude Code 和 Codex 设计的跨平台可视化工作台。但它的杀手级功能在于：**无缝的 SSH 远程 AI 开发**。

你不需要在你的 VPS 或远程开发机上安装任何 AI 助手（如 Claude Code 或 Codex），甚至不需要配置复杂的 Node.js 环境。只需通过 SSH 连入，AstralOps 就会接管一切。

## 核心能力：真正的远程 AI 工作区

当你通过 AstralOps 连接到远程服务器时：

- **零侵入部署**：AstralOps 会自动通过 SSH 将轻量级的 `proxy-agent` 推送到远端。远程机器**完全不需要**安装 Claude Code / Codex，也不需要配置 API 密钥。
- **本地化体验**：AI 助手依然在你性能强大的本地电脑上运行，但它的命令执行（如 `npm test`）、目录浏览、代码搜索 (`grep`/`glob`) 和文件编辑，都会通过 AstralOps 远端执行后端直接发生在远程服务器上。
- **专用远程工具链**：Claude SSH 工作区使用 AstralOps remote MCP tools，Codex SSH 工作区使用 Codex exec-server 转发。远端只需要 AstralOps 上传的轻量 `proxy-agent`，不需要安装 Claude Code / Codex，也不需要镜像完整代码库到本地。
- **绝对掌控**：所有在远程服务器上的高危命令执行和文件修改，都会在你本地的桌面端触发弹窗拦截，必须经过你的点击批准才会真正在远端执行。

## 这是怎么做到的？

AstralOps 构建了一套创新的远程代理架构：

1. **本地 Daemon (Go)**：在你的电脑上运行，负责管理 AI 助手进程、记录历史日志以及提供桌面 UI。
2. **远程 Proxy-Agent (Go)**：一个极小且无依赖的二进制文件。每次连接时由 Daemon 自动通过系统 SSH 上传至远端（支持 Linux/macOS, x86/ARM）并拉起。
3. **JSON-RPC over SSH**：无需在服务器开放任何额外端口。本地 AI 产生的读写意图被转化为结构化请求，直接通过 SSH 的标准输入输出流传递给远端的 `proxy-agent` 执行。

无论是排查线上服务器的诡异 Bug，还是直接在只对内网开放的测试机上开发新功能，AstralOps 都能让 AI 成为你远程排障的得力副手。

## 适用场景

- 代码出于安全合规要求只能留在远程开发机上，本地无法拉取。
- 在配置较低的云服务器或树莓派上开发，跑不动庞大的 Node.js 语言模型工具链。
- 需要 AI 助手直接在包含特定环境变量和数据库的远端运行环境中测试、修改代码。

## 快速上手

```bash
# 确保本地已安装 Node.js 和 Go
npm install
npm run dev
```

启动桌面端后，点击“新建工作区”，选择 **SSH**，输入你的目标服务器地址（支持读取你的 `~/.ssh/config`）和目标目录即可开始！

## 打包桌面客户端

```bash
# 确保本地已安装 Node.js 和 Go
npm install
npm run package:desktop
```

打包脚本会自动识别当前系统和 CPU 架构：

- macOS 会生成当前架构的 `.dmg` 和 `.zip`。
- Linux 会生成当前架构的 `AppImage` 和 `.deb`。
- Windows 会生成当前架构的 portable 和 NSIS 安装包。

产物会输出到：

```text
release/desktop/out/<platform>-<arch>/
```

打包时会自动构建并内置本机 daemon，以及用于 SSH 远端的 Linux/macOS `proxy-agent` helper。Windows 客户端支持 Claude/Codex 的本地和 SSH 工作区任务流，但右侧内置终端当前会被禁用；Linux/macOS 客户端保留内置终端。

建议在目标系统上打对应平台的包：在 macOS 上打 macOS 包，在 Linux 上打 Linux 包，在 Windows 上打 Windows 包。跨平台打包可能受 Electron、系统签名、安装器依赖限制。

## CI 发布流程

日常开发提交进入 `dev` 分支。`main` 作为发布分支，只通过 `dev -> main` 的 PR 合并更新。

合并到 `main` 后，GitHub Actions 会执行 release 计划：

- 如果从上一个 `v*` tag 到当前提交之间只有 README、Markdown、`docs/`、`.github/`、LICENSE、`.gitignore` 等非产品代码变更，则跳过发版。
- 如果包含产品代码变更，则自动生成下一个版本号、打包 macOS/Linux/Windows 桌面客户端，并创建 GitHub Release。
- 第一次发版在没有历史 tag 时使用根 `package.json` 里的版本号；之后基于最新 `vX.Y.Z` tag 自动递增。

版本递增规则：

- 提交信息包含 `BREAKING CHANGE:` 或 Conventional Commit 的 `!` 标记时递增 major。
- 提交信息包含 `feat:` 时递增 minor。
- 其它产品代码变更默认递增 patch。

发布产物会附带 `SHA256SUMS.txt`。当前 CI 产物默认未做 macOS notarization 或 Windows Authenticode 签名；公开分发前需要补充对应签名密钥和 workflow 配置。

## 技术栈与目录结构

```text
apps/desktop/     桌面界面 (Electron + React)
daemon/           本地核心层 (Go)：负责管理 AI 会话、远端工具转发和 SSH 隧道
proxy-agent/      远端代理 (Go)：通过 SSH 执行文件操作和命令的无依赖单体
protocol/         通讯协议 (TypeScript)：JSON-RPC 定义
```

## 安全与隐私

- 所有的 API Keys、AI 思考过程和聊天记录均保存在你的本地电脑 `~/.AstralOps`。
- SSH 连接完全复用你本地系统的 `ssh` 进程。AstralOps **绝不**触碰、读取或保存你的 SSH 私钥。
- 绝无任何形式的云端遥测和数据收集。

## 许可证

[AGPL-3.0](./LICENSE)
