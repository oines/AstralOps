# AstralOps 远程控制 Mesh 架构

最后更新：2026-05-27

## 总览

AstralOps 未来不是“云端同步工作区数据”的系统，而是一个由云账号托管连接的、端到端加密的多设备远程控制 Mesh。

核心模型是：

```text
账号
  -> 多台设备
  -> 多组可信控制关系
  -> Controller 与 Desktop Host 之间建立加密点对点控制会话
```

云服务和任意 relay 节点只负责账号、设备发现、在线状态、配对授权、信令和必要时的流量中继。它们不能查看 prompt、session、workspace、event payload、approval 内容、PTY 输出、SSH 信息或文件树。

Desktop Host 永远是执行权威。workspace/session/event JSONL、Claude/Codex runtime、SSH key、SSH workspace 操作、PTY 进程、pending interaction、本地文件和凭据都留在 Host 机器上。

## 设备角色

### Desktop App

每个 Desktop App 同时是 Controller 和 Host：

```text
Desktop = Controller UI + Local Host/Core
```

作为 Controller，它可以远程控制另一台可信 Desktop Host。

作为 Host，它通过端到端加密控制通道，把 AstralOps Core 能力暴露给可信 Controller。

Desktop Host 拥有：

```text
workspace/session/event JSONL
Claude/Codex runtime
SSH workspace manager
PTY manager
pending interaction projection
notification intent generation
trusted device policy
local files and credentials
```

### Mobile App

Mobile 只做 Controller：

```text
Mobile = Controller UI only
```

Mobile 不运行 daemon，不保存 workspace/session/event 数据，不运行 Claude/Codex，不持有 SSH key，也不直接读写文件或执行命令。

但 Mobile 不是功能阉割版。它应该是完整远程 UI。只要 Host 授权，Mobile 可以使用完整 AstralOps 能力：

```text
管理 workspace
管理 session
发送 prompt
响应 Ask / approval / plan
通过 Host 浏览和修改文件
通过 Host 执行命令
通过 Host 打开 / attach / 输入 PTY
通过 Host 操作 SSH workspace
管理 Host 设置和 trusted devices
```

Desktop 和 Mobile 的区别是执行位置，不是功能完整度：

```text
Controller 发送意图
Host 检查权限
Host 执行动作
Controller 渲染结果
```

## 云端边界

云端是账号和连接 broker，不是 AstralOps 大脑。

云端可以保存：

```text
account_id
device_id
device name / kind
device public key
capabilities
online / offline presence
pairing / trust metadata
connection/session metadata
relay routing metadata
```

云端不能保存或查看：

```text
workspace 名称或路径
session 内容
AstralEvent.normalized
AstralEvent.raw
prompt 文本
assistant 输出
approval / ask / plan 内容
PTY 输出
文件树或文件内容
命令输出
SSH 细节
本地凭据或 SSH key
```

Relay 节点是不可信转发器。Relay payload 必须是不透明加密帧：

```json
{
  "connection_id": "conn_x",
  "from_device_id": "dev_a",
  "to_device_id": "dev_b",
  "ciphertext": "..."
}
```

Relay 日志不能打印明文 payload，也不能打印解密后的协议消息。

## 端到端加密控制通道

端到端加密发生在 Controller 设备和 Desktop Host 之间：

```text
Controller Device <==== E2EE Control Channel ====> Desktop Host
```

云端和 relay 可以终止自己的 TLS 连接，但业务 payload 在到达云端/relay 之前，必须已经由设备层加密。

必须满足：

```text
每台设备有本地 device identity keypair。
私钥只留在设备本地。
云端只保存 public key 和 trust metadata。
Controller 与 Host 通过云端信令建立临时加密会话。
Core API 消息、event subscription payload、PTY stream frame 都走这个加密会话。
云端和 relay 不能解密业务 payload。
```

推送通知不能包含 session 或任务内容。推送最多只能提示 AstralOps 有新活动；App 打开后必须通过 E2EE channel 拉取详情。

## Mesh 模型

一个账号下可以有多台设备：

```text
Desktop A: can_host=true,  can_control=true
Desktop B: can_host=true,  can_control=true
Mobile C:  can_host=false, can_control=true
Mobile D:  can_host=false, can_control=true
```

同账号设备组成私有控制 Mesh，但“可发现”不等于“可控制”。

控制权限应该建模为 device-to-device grant：

```json
{
  "host_device_id": "dev_desktop_b",
  "controller_device_id": "dev_mobile_c",
  "scope": "full",
  "status": "trusted",
  "created_at": "2026-05-27T00:00:00Z"
}
```

默认产品策略：

```text
同账号设备可以互相发现。
新 Controller 设备第一次控制 Desktop Host 前需要显式批准。
用户自己的可信 Mobile 可以获得 full control。
Host 可以随时撤销任意 Controller。
Host 永远是每个动作的最终权限判断者。
```

多 Controller 同时连接同一 Host 时：

```text
多个 Controller 可以同时查看同一台 Host。
Host 对每个 action 做当前状态和 trust policy 校验。
同一个 pending interaction 只能成功 resolve 一次。
过期响应由 Host 拒绝。
PTY 可以支持多个 viewer，但默认只有一个 active writer。
Host 可以断开或撤销某个 Controller session。
```

## 踢出设备与立即断开

踢出设备必须由 Host 本地强制执行，不能只依赖云端把 trust 标成 revoked。

安全语义是：

```text
revoke trust grant
立即关闭该 Controller 的所有 active connection
```

典型流程：

```text
Controller A 正在控制 Desktop Host B

Host B 用户点击“移除设备 A”
  -> Host B 本地 trust store 标记 A revoked
  -> Host B 关闭 A 的所有 control sessions
  -> Host B 清理 A 的 event subscription / pending request / stream attach
  -> Host B 释放 A 持有的 PTY active writer lock
  -> Host B 广播 trust revoked 状态给其他仍可信 Controller
  -> Host B 通知云端更新 trust metadata
```

Host 端需要维护：

```text
trusted_devices
active_control_sessions
active_terminal_sessions
```

建议内部模型：

```text
TrustGrant
  host_device_id
  controller_device_id
  status: trusted | revoked
  capabilities
  created_at
  updated_at
  revoked_at

ControlSession
  session_id
  controller_device_id
  connection_id
  e2ee_session_id
  status: active | closing | closed
```

Host 本地 API 可以是：

```text
POST /v1/trust/devices/:device_id/revoke
```

执行步骤：

```text
1. 将 trust grant 标记为 revoked，并写入 revoked_at。
2. 找到 controller_device_id == device_id 的所有 active control sessions。
3. 对每个 session 发送 encrypted close frame，reason=trust_revoked。
4. 关闭 transport。
5. 清理该设备的 event subscription、pending request、PTY attach。
6. 如果该设备是某个 PTY 的 active writer，释放 writer lock。
7. append 本地 audit event，例如 control.trust.revoked。
8. 通知云端同步 trust metadata。
```

被踢出的 Controller 收到连接关闭后，UI 显示：

```text
该设备已不再被此 Host 信任
```

之后即使它再次尝试连接，也必须失败。Host 接受每条远程连接前都要校验：

```text
controller_device_id 是否 trusted
capabilities 是否允许本次 action
grant version 是否匹配
revoked_at 是否为空
```

云端和 relay 可以辅助阻止后续连接：

```text
Cloud 收到 trust revoked
  -> 标记 grant revoked
  -> 拒绝后续 signaling
  -> 给相关设备发送 trust update
  -> relay 关闭对应 connection_id
```

但云端和 relay 不是安全边界。安全边界必须是 Host 本地 trust store 和 active session registry。

## 运行路径

### Desktop 本机使用

```text
Desktop UI
  -> LocalCoreClient
  -> LocalHttpControlChannel
  -> local daemon/Core
```

### Desktop 控制 Desktop

```text
Desktop A UI
  -> RemoteCoreClient
  -> RemoteEncryptedControlChannel
  -> cloud signaling / relay
  -> Desktop B daemon/Core
```

### Mobile 控制 Desktop

```text
Mobile UI
  -> RemoteCoreClient
  -> RemoteEncryptedControlChannel
  -> cloud signaling / relay
  -> Desktop Host daemon/Core
```

所有远程路径里，Host 执行，Controller 渲染。

## 远控能力

Capability 描述可信 Controller 可以请求 Host 做什么。它不应该按 Desktop/Mobile 硬砍功能。可信 Mobile 也可以拥有 full control。

建议内部 capability 分组：

```text
core.read
core.control
session.edit
interaction.respond
workspace.files.read
workspace.files.write
workspace.exec
terminal.open
terminal.input
host.manage
```

含义：

```text
core.read
  查看 workspace 列表、session 列表、session view、transcript projection、agent 状态、queue、pending interaction。

core.control
  发送 prompt、中断 turn、取消/steer queued prompt、fork/delete session。

session.edit
  编辑最后一条用户消息并由 Host/Core 执行 rollback/resend。被替换的旧 turn range 必须从 transcript 和 pending interaction projection 中隐藏，旧 approval/ask 响应必须由 Host 拒绝为 stale。

interaction.respond
  回复 Ask，批准/拒绝 plan，批准/拒绝 command/file/permission request。

workspace.files.read
  通过 Host 浏览文件。SSH workspace 中，由 Host 发起 SSH 读取。

workspace.files.write
  通过 Host 创建、编辑、删除、移动文件或应用 diff。SSH workspace 中，由 Host 发起 SSH 写入。

workspace.exec
  通过 Host 执行 workspace command，包括 agent command approval。

terminal.open
  打开或 attach Host 拥有的 PTY。

terminal.input
  向 Host 拥有的 PTY 发送原始按键输入。

host.manage
  创建/删除 workspace，连接/断开 SSH workspace，管理设置，管理 trusted devices，撤销会话。
```

UI 可以展示更简单的模式：

```text
完整控制
有限控制
仅查看
临时协助
```

但 Host 内部应该按细粒度 capability 执行权限判断。

## PTY 架构

远程终端是 Host-owned PTY，不是普通 AstralEvent stream。

```text
Controller UI
  -> TerminalClient
  -> encrypted terminal stream
  -> Host PTY manager
  -> local shell / SSH shell / TUI process
```

SSH workspace 的 PTY 路径是：

```text
Controller
  -> Desktop Host
  -> Host SSH connection
  -> remote SSH PTY
```

PTY 输出不能作为普通 event JSONL 存储。JSONL 最多记录生命周期事件，例如 opened、closed、failed、attached。高频 ANSI 输出留在 stream channel。

未来 PTY manager 形态：

```text
terminal.open -> terminal_id
terminal.attach
terminal.input
terminal.resize
terminal.output stream
terminal.close
```

断线行为：

```text
Host 可以在短时间 retention window 内保留 PTY session。
Controller 重连后可以重新 attach。
多个 viewer 可以 attach。
默认只有一个 Controller 拥有 active input。
```

## 代码形态

目标代码结构：

```text
protocol
  共享 event / API / capability 类型。

core-client
  CoreClient
  LocalCoreClient
  RemoteCoreClient
  TerminalClient
  ControlChannel
  LocalHttpControlChannel
  RemoteEncryptedControlChannel

apps/desktop
  Electron shell
  Controller UI
  本机 Host 启动/连接
  远程 Host 选择

apps/mobile
  Controller UI
  只实现 RemoteCoreClient
  不带 daemon/Core/runtime

daemon
  Host/Core
  JSONL event store
  workspace/session runtime
  Claude/Codex integrations
  SSH manager
  PTY manager
  pending interaction state
  trusted-device enforcement

cloud
  auth/account
  device registry
  presence
  pairing/trust
  signaling
  relay
```

当前实现已经开始建立边界：

```text
apps/desktop/src/api.ts
  CoreClient
  ControlChannel
  LocalCoreClient
  LocalHttpControlChannel
  TerminalClient
```

后续实现应该扩展这个边界，不要把远程逻辑直接塞进 UI 组件。

## 实施阶段

### Phase 1 - Host Identity

增加本地 Host/device identity：

```text
device_id
device_name
device_kind: desktop | mobile
device_public_key
local private key in OS keychain/keyring
capabilities
```

Desktop daemon 暴露本地 Host info endpoint：

```text
GET /v1/host
```

返回 Host identity、platform、features、capabilities。

### Phase 2 - Account Device Registry

云端保存设备和在线状态：

```text
account_id
device_id
device_name
kind
public_key
capabilities
online_status
last_seen
```

不上传 workspace/session/event 数据。

### Phase 3 - Pairing and Trust

实现 device-to-device trust grant：

```text
controller_device_id
host_device_id
scope
capabilities
status
created_at
revoked_at
```

Host 在执行任何远程动作前，必须本地强制校验 trust。

### Phase 4 - Remote Encrypted Control Channel

增加：

```text
RemoteCoreClient
RemoteEncryptedControlChannel
encrypted request/response frames
encrypted event subscription frames
encrypted terminal stream frames
reconnect and resume semantics
```

云端 signaling 负责协商连接。Relay 只转发加密帧。

### Phase 5 - PTY Attach Manager

把当前“一条 WebSocket 对应一个 PTY”的语义升级成 Host-owned terminal session：

```text
open
attach
detach
input
resize
close
retention timeout
single active writer
multi viewer
```

### Phase 6 - Mobile Controller

Mobile 用同一套远控协议构建完整 Controller UI：

```text
device login
device list
Host selection
session/workspace UI
approval/Ask UI
terminal UI
settings/trust UI
```

Mobile 不包含 Host daemon 和 runtime。

## 非目标

这些明确不属于当前架构目标：

```text
云端 event sync
云端 workspace storage
云端可见 transcript storage
云端 agent execution
云端 session projection
本地 event 加密
把 JSONL 换成 SQLite
让 Controller 设备直接访问 SSH key 或 Host 文件
把 PTY 字节输出塞进 AstralEvent JSONL
```

## 核心不变量

AstralOps 必须始终保持这个不变量：

```text
Cloud 是账号入口和 mesh 路由器。
Relay 是不透明 packet forwarder。
Desktop Host 是执行权威。
Controller 设备是完整远程 UI。
业务 payload 在 Controller 和 Host 之间端到端加密。
```
