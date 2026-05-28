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
Core API 消息、event subscription payload、附件/媒体数据帧、PTY stream frame 都走这个加密会话。
云端和 relay 不能解密业务 payload。
```

推送通知不能包含 session 或任务内容。推送最多只能提示 AstralOps 有新活动；App 打开后必须通过 E2EE channel 拉取详情。

## 远控传输策略

远控 v1 使用 LAN-first relay fallback。传输路径不参与信任判断：

```text
Transport is not trust.
LAN direct 和 relay 只是 packet path。
所有路径都必须跑同一套设备认证 E2EE 握手。
```

Desktop Host 在本机网络上提供一个远控 LAN listener，并在同一个端口监听 UDP discovery。Controller 需要 LAN 发现时，向局域网广播一个 discovery request，Host 只单播回复候选地址：

```text
UDP request:
  type=astralops.discovery.request
  version=astralops-control-v1

UDP response:
  type=astralops.discovery.response
  version=astralops-control-v1
  candidate.device_id
  candidate.account_id_hash
  candidate.public_key_fingerprint
  candidate.host
  candidate.port
```

当前 daemon MVP 通过显式环境变量开启 LAN listener：

```text
ASTRALOPS_CONTROL_LISTEN=0.0.0.0:43900
```

开启 LAN listener 时，daemon 默认开启 UDP discovery。开发或排障时可以关闭：

```text
ASTRALOPS_CONTROL_DISCOVERY=0
```

LAN listener 只暴露远控所需的最小接口：

```text
GET /v1/host
GET /v1/control/ws
```

它不能暴露完整本地 HTTP API，例如 workspace/session/settings/events 等本地端点。`/v1/host` 只返回 public device identity、platform、features 和 capabilities；业务数据仍必须通过加密 control channel 获取。

开发测试时可以临时打开：

```text
ASTRALOPS_DEV_REMOTE_PAIRING=1
```

这只用于两台本机设备在开发环境快速写入 Host trust grant。正式 pairing 必须由账号设备注册、已有可信设备或目标 Host 明确批准驱动，不能留下未授权的 LAN trust 写入口。

Controller 侧可以用开发命令查看 LAN 候选 Host：

```text
go run ./daemon control-client discover --timeout 3s --port 43900
```

discover 只返回 `LanHostCandidate`，例如 device id、public key fingerprint、LAN 地址和端口。它不能自动授信、不能自动 pair、不能绕过 Host Gateway。后续连接仍必须进入 `/v1/control/ws` 并完成 E2EE 握手。

当前开发客户端在 `pair` 成功后，会把 Host public identity 记入本机 `known_hosts.json`。这只是 Controller 侧的 Host 身份缓存，用来校验后续 LAN discovery candidate；真正的执行授权仍然由 Host 本地 trust store 决定。

```text
go run ./daemon control-client known-hosts
go run ./daemon control-client workspaces --discover --host-device-id <host_device_id>
go run ./daemon control-client sessions --discover --host-device-id <host_device_id> --workspace-id <workspace_id>
go run ./daemon control-client session-view --discover --host-device-id <host_device_id> --session-id <session_id>
go run ./daemon control-client events --discover --host-device-id <host_device_id> --session-id <session_id> --limit 50
go run ./daemon control-client trust-list --discover --host-device-id <host_device_id>
go run ./daemon control-client smoke --discover --host-device-id <host_device_id>
go run ./daemon control-client smoke --discover --host-device-id <host_device_id> --trust-list
go run ./daemon control-client smoke --discover --host-device-id <host_device_id> --workspace-id <workspace_id> --session-id <session_id> --sessions --session-view --events --event-subscription
go run ./daemon control-client smoke --discover --host-device-id <host_device_id> --workspace-id <workspace_id> --path . --stream-path large.log --workspace-write-smoke --exec-command "pwd" --terminal
go run ./daemon control-client smoke --discover --host-device-id <host_device_id> --session-id <session_id> --attachment-path ./clip.png
go run ./daemon control-client smoke --discover --host-device-id <host_device_id> --session-id <session_id> --media-event-seq <event_seq> --media-id <media_id>
```

开发客户端可以同时传 `--discover --host <relay_or_host_url>`。这种模式会先按 `host_device_id` 尝试已知 Host 的 LAN candidate；LAN discovery、短超时 Host identity validation 或 control channel dial/handshake 失败时，回退到显式 `--host`。`--host` 在当前 MVP 里可以是手填远控 URL，后续也可以是 relay URL；fallback 不改变 E2EE 握手和 Host trust store 校验，并且 fallback Host identity 必须匹配 LAN candidate 对应的 Host。

`control-client smoke` 只走 `/v1/control/ws` 的 E2EE request/response，不访问本地 workspace/session/settings/events HTTP API。默认只验证 `core.read.workspaces`；可选 `--sessions` 验证 `core.read.sessions`，可选 `--session-view` 验证指定 `--session-id` 的 `core.read.session_view`，可选 `--events` 验证 `core.read.events` 的窗口读取，可选 `--event-subscription` 验证 `core.subscribe.events` replay event frame；两者都只走 E2EE control channel。可选 `--trust-list` 验证 `host.trust.list` 的非破坏性 Host management 查询；传入 `--workspace-id` 后会验证 `workspace.files.read`，可选 `--stream-path` 验证 `workspace.files.stream` 的 chunked E2EE frames，可选 `--workspace-write-smoke` 在 Host workspace 临时目录中验证 `workspace.files.write/apply_patch/move/delete` 并清理，可选 `--exec-command` 验证 `workspace.exec`，可选 `--terminal` 验证 Host-owned PTY 的 `terminal.open/attach/input/output/close` E2EE 链路。传入 `--session-id --attachment-path` 后会验证 `attachment.ingest.start/chunk/finish`，Controller 文件会通过 E2EE channel 分片上传到 Host-owned attachment store。传入 `--session-id --media-event-seq --media-id` 后会验证 transcript media reference 的 `media.stream` E2EE frames，并返回 resume_token/bytes/chunks 等摘要。stream/attachment/media/workspace-write/terminal/trust/session/events/event-subscription smoke 只输出摘要，不打印文件内容、事件正文、PTY 输出内容或 Host path。

UDP discovery 只用于发现候选地址，不能授予信任。Controller 收到 LAN response 后，必须用本地 trust store 或云端 device registry 校验 `device_id` 和 public key fingerprint。真正连接成功的条件仍然是：

```text
LAN transport connected
  -> E2EE handshake succeeds
  -> Host public key matches trusted device
  -> Host trust grant / trust_epoch is valid
```

连接某台 Host 时，Controller 使用简单规则：

```text
if host_device_id has LAN candidate:
  try LAN for a short timeout
  if LAN E2EE handshake succeeds:
    use LAN
  else:
    use relay
else:
  use relay
```

这个判断是 per Controller-Host pair 的。一个账号的 mesh 里可以同时存在：

```text
Phone -> Home Desktop: LAN
Phone -> Office Desktop: relay
Laptop -> Home Desktop: LAN
Tablet -> Home Desktop: relay
```

v1 明确不做：

```text
public direct
NAT punching
STUN/TURN
WebRTC
复杂 candidate 竞速
运行中 transport migration
```

如果未来需要更强穿透能力，再引入 ICE/WebRTC/QUIC。当前目标是低复杂度、低延迟优先、relay 自动兜底。

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

## Onboarding 流程

Onboarding 分两层：

```text
账号 onboarding
设备信任 onboarding
```

云账号只负责发现设备和发起配对请求。登录账号不等于获得控制权；新设备必须被已有可信设备或目标 Host 明确批准。

### 第一次打开 Desktop

```text
1. 登录或创建云账号。
2. 本机生成 device identity keypair。
3. 用户给这台设备命名。
4. 注册 device public key 到云端 device registry。
5. 选择这台 Desktop 是否允许被远程控制。
6. 如果允许被远程控制：
   - 启动 Host control listener。
   - 开启 LAN UDP discovery。
   - 准备 relay fallback。
   - 创建本地 Host trust store。
7. 创建第一个 workspace。
8. 进入应用。
```

如果用户关闭“允许被远程控制”，这台 Desktop 仍然可以作为 Controller 控制其他 Host，但不会接受远程连接。后续可以在本机设置中开启。

### 新增 Mobile Controller

```text
1. Mobile 登录同一云账号。
2. Mobile 本机生成 device identity keypair。
3. Mobile 注册 device public key 到云端。
4. 云端向已有可信设备或目标 Desktop Host 发出新设备加入请求。
5. Desktop 显示配对确认：
   - 设备名
   - 设备类型
   - public key fingerprint
   - 请求的控制范围
6. 用户批准或拒绝。
7. 批准后，Host 写入 device-to-device trust grant。
8. Mobile 设备列表显示可控制的 Desktop Host。
```

MVP 权限可以只有：

```text
full control
denied
```

后续再扩展：

```text
view only
terminal only
temporary access
```

### 新增第二台 Desktop

```text
1. 新 Desktop 登录云账号。
2. 本机生成 device identity keypair。
3. 用户给设备命名。
4. 用户选择：
   - 允许被远程控制。
   - 只作为 Controller。
5. 由已有可信设备或目标 Host 批准加入。
6. 如果允许被远控，启动 Host listener、LAN UDP discovery、relay fallback。
```

Desktop 可以同时是 Controller 和 Host。是否允许被控制是本机选择，不由云端默认开启。

### 配对确认 UI

配对确认必须展示可核验身份，而不是只显示云账号名：

```text
新设备请求加入
设备名：Yuxin's iPhone
类型：Mobile
指纹：ABCD 1234 EFGH 5678

允许它控制这台 Desktop？
[允许完整控制] [拒绝]
```

后续如果支持更细粒度权限，可以在这里选择 capability scope。MVP 不需要复杂权限矩阵。

### 设备列表

账号设备列表展示发现状态，但控制权仍以 Host 本地 trust grant 为准：

```text
MacBook Pro
  当前设备
  可被控制
  在线

Home PC
  可被控制
  在线
  LAN 可用

iPhone
  Controller
  在线

Office Desktop
  离线
```

用户可以：

```text
重命名本机设备
允许/关闭本机被远控
查看可信设备
撤销某个 Controller 对本机的访问
查看连接路径：LAN / relay
```

### 远控连接体验

用户选择一个 Desktop Host 后，产品可以显示简化状态：

```text
正在连接 Home Mac
  尝试局域网
  建立加密通道
  验证设备身份
  加载 workspace/session
```

如果 LAN 不可用：

```text
局域网不可用，正在通过安全中继连接
```

用户不需要手动选择 LAN 或 relay。系统按 LAN-first relay fallback 自动处理。

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
6. 如果该设备是某个 PTY 的 active writer，释放 writer lock，并让下一台仍可信且具备 `terminal.input` 能力的 Controller 由 Host 在 input/resize/close 时重新认领。
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
attachment.ingest
media.read
media.download
media.stream
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
  发送 prompt、中断 turn、取消/steer queued prompt、fork/delete session。发送 prompt 时必须由 Host/Core 明确判定 session input mode，而不是由 Controller UI 自行猜测。queued prompt 管理由 `core.control.queue.cancel` 和 `core.control.queue.steer` 暴露，Controller 只能传 session_id + queue_id；Host 负责确认 queued turn 是否仍存在并落 queue.cancelled / queue.steered 事件。session fork/delete 管理由 `core.control.session.fork` 和 `core.control.session.delete` 暴露，Host 负责复用本地 session 语义创建 fork projection、停止 runtime、清理 queue 并落 session.deleted。

session.edit
  编辑最后一条用户消息并由 Host/Core 执行 rollback/resend。被替换的旧 turn range 必须从 transcript 和 pending interaction projection 中隐藏，旧 approval/ask 响应必须由 Host 拒绝为 stale。

interaction.respond
  回复 Ask，批准/拒绝 plan，批准/拒绝 command/file/permission request。

attachment.ingest
  Controller 把本地选择或粘贴的文件通过 E2EE control request 发送给 Host。中小附件可单次 ingest；大文件用 start/chunk/finish。Host 写入自己的 upload store，并返回 Host-owned attachment handle。Controller 不能把自己的本机 path 直接交给远端 Host 当可读路径。

media.read
  读取 transcript 中由 event_seq + media_id 引用的 Host-owned 媒体资源。Controller 只能拿到 Host 通过能力检查后返回的媒体内容或预览数据。

media.download
  请求 Host 以下载语义返回媒体资源，文件名、MIME type、大小等元数据由 Host/Core 决定。

media.stream
  面向大文件、生成中图片、未来视频或渐进式预览的媒体流。数据帧和 PTY 一样走 E2EE channel，relay 只转发密文。Host 返回 resume_token；Controller 可以在新的控制连接上用 resume_token + offset 重新发起 stream 来恢复读取，也可以用 media.stream.cancel 取消同一 control connection 上的 active stream。v1 的恢复是 Host descriptor + offset replay，不是 relay 持久化、明文缓存或 transport migration。

workspace.files.read
  通过 Host 浏览目录或读取文件内容。SSH workspace 中，由 Host 发起 SSH 读取。v1 返回目录列表或中小文件的 base64 内容；大文件用 `workspace.files.stream` action，仍复用该 capability。

workspace.files.write
  通过 Host 创建、覆盖、精确文本编辑、删除或移动 workspace root 内的文件路径。SSH workspace 中，由 Host 发起 SSH 写入、删除或移动。复杂大文件流式读写仍作为独立能力后续扩展，不塞进普通 write response。

workspace.exec
  通过 Host 在 workspace root 内执行 command。SSH workspace 中，由 Host 发起 SSH exec。是否允许执行由 Host/Core 的 `workspace_exec_policy` 判断，Controller 不能自行绕过。

terminal.open
  打开或 attach Host 拥有的 PTY。

terminal.input
  向 Host 拥有的 PTY 发送原始按键输入。

host.manage
  管理 Host-owned 控制面能力。v1 先只开放 `host.trust.list` 和 `host.trust.revoke`，用于通过 E2EE control channel 查看 trusted devices、撤销某个 Controller，并触发 Host 本地立即断开和 terminal writer lock 释放。创建/删除 workspace、连接/断开 SSH workspace、settings 和 updates 不塞进这个 v1 action。
```

UI 可以展示更简单的模式：

```text
完整控制
有限控制
仅查看
临时协助
```

但 Host 内部应该按细粒度 capability 执行权限判断。

## Session Input 语义

远控 v1 不需要把 `session.input` 拆成单独 capability；它属于 `core.control`。但它的行为语义必须明确。

Controller 发来的输入必须由 Host/Core 归一成以下模式之一：

```text
start
  session 空闲时启动一个新 turn。

queue
  session 正在运行，但本次输入应该排队，等待当前 turn 完成后再执行。

steer
  session 正在运行，本次输入用于引导当前任务。
  Codex 当前对应 turn/steer。
  Claude 当前实现更接近 interrupt 当前 turn 后用新输入接上。
```

Controller UI 可以只显示“发送”或“继续输入”，但远控协议和 Host 日志不能含糊。多 Controller 同时控制同一 Host 时，Host/Core 是唯一可以决定 start、queue、steer 的地方。

`core.control.session_input` 的 response 必须带 Host/Core 最终决策：

```text
mode: start | queue | steer
queue_id: only when mode=queue
queued / steered: legacy compatibility flags
```

queued input 后续控制：

```text
core.control.queue.cancel(session_id, queue_id)
  -> queue.cancelled

core.control.queue.steer(session_id, queue_id)
  -> queue.steered
```

session 后续控制：

```text
core.control.session.fork(session_id, event_seq)
  -> { session }
  event_seq 必须指向源 session 已完成 turn 的最终 assistant reply。Host/Core 负责校验 fork anchor、创建 fork session、投影安全 transcript，并调用 agent runtime 的 fork 能力。

core.control.session.delete(session_id)
  -> session.deleted
  Host/Core 负责停止对应 runtime、清空 queued prompt、删除本地 session，并让该 session 的后续控制请求返回 session_not_found。
```

## 本机 Shell 设置

当前 `AppSettings`、桌面主题、通知偏好、窗口效果、日志目录、自动更新检查/安装，都属于本机 desktop shell concern。

远控 v1 不暴露这些能力：

```text
settings.read
settings.write
updates.check
updates.install
```

未来如果要做远程设备管理，再单独设计 Host management capability。不要把当前 desktop 本机设置默认纳入远控核心能力。

## 附件和媒体资源

附件和 transcript 媒体是 Host-owned resource，不是跨设备共享路径。

当前本地实现中，`message.user.normalized.attachments` 和 `message.media.normalized` 可以包含 `path` 或 `saved_path`。这些路径只表示 Host 本机上的执行/读取引用：

```text
Host local upload path
Host local generated media path
Host runtime-readable path
```

远程 Controller，包括 Mobile，不能把这些路径当作自己可以访问的文件路径。远程 UI 只能使用：

```text
event_seq
media_id
name
mime_type
size
kind
status
```

读取媒体必须通过 Host/Core：

```text
Controller
  -> media.read(event_seq, media_id)
  -> E2EE data channel
  -> Host media gateway
  -> Host local file
```

手机或另一台 Desktop 发送附件时，流程必须是：

```text
Controller selects file / paste image
  -> attachment.ingest metadata
  -> encrypted upload payload
  -> Host upload store
  -> Host returns attachment_id/media_id
  -> core.control send input with Host-owned attachment handle
```

`attachment.ingest` v1 支持两种 Host-owned 上传方式：

```text
中小附件:
attachment.ingest(content_base64) -> Host-owned attachment handle

大文件:
attachment.ingest.start(metadata)
attachment.ingest.chunk(seq, offset, data_base64)
attachment.ingest.finish(upload_id) -> Host-owned attachment handle
```

chunked ingest 仍然走 encrypted control request/response，不开放明文 HTTP upload URL。Host 用 seq/offset 校验顺序写入自己的 upload store，finish 后才返回可用于 `core.control.session_input` 的 attachment handle。远程 `core.control.session_input` 只接受 Host-owned attachment handle，不接受 Controller 本地路径。

禁止的模型：

```text
Controller path 直接传给 Host runtime
云端保存明文附件
relay 解密或缓存明文媒体
把 media URL 做成云端可见静态资源
移动端直接访问 Desktop/SSH 文件路径
```

对于 SSH workspace，附件仍然先进入 Desktop Host。是否需要 staging 到 SSH remote，由 Host/Core/runtime adapter 决定；Controller 不接触 SSH key，也不直接写 SSH remote。

本地 HTTP media endpoint 只是 LocalCoreClient 的实现细节。远控时必须通过 RemoteEncryptedControlChannel 暴露为 `media.read` / `media.download` / `media.stream` capability。

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

PTY manager 目标形态：

```text
terminal.open -> terminal_id
terminal.attach
terminal.input
terminal.resize
terminal.output stream
terminal.close
```

当前 daemon 已落地最小 Host-owned PTY 控制面：

```text
terminal.open
terminal.attach
terminal.detach
terminal.input
terminal.resize
terminal.close
terminal.output stream over E2EE control channel
single active writer
multi viewer
opened/attached/detached/closed lifecycle events only
trust revocation releases active writer lock
```

这些 action 仍然经过 Host trust store 和 capability 校验。`terminal.attach` 必须发生在已完成握手的 encrypted control WebSocket 上，因为 PTY 输出只能回到这条 E2EE channel。`terminal.input`、`terminal.resize`、`terminal.close` 使用 `terminal.input` capability，因为它们都会改变 Host 侧 PTY 状态。PTY 输出不进入 JSONL，只有 opened、attached、detached、closed lifecycle event 会落盘。

断线行为：

```text
Host 可以在短时间 retention window 内保留 PTY session。
Controller 重连后可以重新 attach。
多个 viewer 可以 attach。
默认只有一个 Controller 拥有 active input。
撤销 trusted device 会清空该设备持有的 writer_device_id；释放后的 writer 只能由下一台仍可信且具备 terminal.input capability 的 Controller 经 Host/Core 认领。
没有 viewer 的 terminal session 会启动 retention timeout。
retention 到期后 Host 关闭 PTY 并记录 closed(reason=retention_timeout)。
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

当前 daemon MVP 可以先把本机 device identity 写入本地 Host 私有文件，并用 0600 权限保护。这个文件仍然只在本机，不进入云端或 relay；后续可以把同一模型的 private key 存储替换成 OS keychain/keyring，不改变 `/v1/host` 和 trust grant 协议。

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

当前 daemon 已落地：

```text
host.trust.list over E2EE control channel
host.trust.revoke over E2EE control channel
local trust revoke HTTP endpoint
immediate active control session close
terminal active writer release on revoke
```

批准新设备后，Host 可以写入本地审计事件：

```text
control.trust.granted
```

### Phase 4 - Onboarding and Pairing UX

实现最小 onboarding：

```text
Desktop 登录账号
Desktop 创建设备身份
Desktop 选择是否允许被远控
Mobile/Desktop 新设备登录账号
已有可信设备批准新设备
设备列表显示 Host 可用状态
```

登录账号只能发现设备，不能绕过配对授权。

### Phase 5 - Remote Encrypted Control Channel

增加：

```text
RemoteCoreClient
RemoteEncryptedControlChannel
authenticated device handshake
encrypted request/response frames
encrypted event subscription frames
encrypted attachment/media frames
encrypted terminal stream frames
reconnect and resume semantics
```

`core.subscribe.events` v1 是最小事件订阅协议：Controller 先用 `core.read.events` 按 `after_seq` 拉取窗口，再在同一条 E2EE WebSocket 上订阅 Host event frame。为了避免读窗口和订阅之间的竞态，订阅请求可以带 `after_seq + replay_limit`，Host 会先发送符合过滤条件的 replay frame，再发送后续 live frame。`core.unsubscribe.events(stream_id)` 取消订阅；连接断开、设备踢出信任或 control session 关闭时，Host 必须清理该连接上的 event subscription。

当前 daemon 的最小控制通道入口：

```text
GET /v1/control/ws
```

握手和帧语义：

```text
hello
  controller_device_id
  controller_public_key
  controller_ephemeral_key
  client_nonce
  Ed25519 signature

hello_ack
  host_device_id
  host_public_key
  host_ephemeral_key
  server_nonce
  connection_id
  Ed25519 signature

sealed
  seq
  nonce
  AES-GCM ciphertext
```

会话密钥由 X25519 临时密钥交换派生。`request/response/close` 等业务帧只能放进 `sealed`，不能以明文 JSON 发送。Host 收到 request 后必须覆盖或校验 `controller_device_id`，然后进入 Host Gateway 执行 capability/trust 检查。

Host 本地维护 active control sessions。撤销某个 Controller trust grant 时，Host 必须发送加密 close frame 并关闭该 Controller 的所有 active control sessions。

云端 signaling 负责协商连接。Relay 只转发加密帧。

### Phase 6 - LAN-first Relay Fallback

实现远控传输 v1：

```text
Desktop Host LAN listener
remote-control-only HTTP surface
dev-only pairing path
LAN UDP discovery
LAN candidate validation
short LAN connection timeout
relay fallback
same E2EE handshake on LAN and relay
```

不实现 NAT punching、WebRTC、STUN/TURN 或 transport migration。

### Phase 7 - Attachment and Media Gateway

把附件和 transcript 媒体收口到 Host gateway：

```text
已落地:
attachment.ingest
Host-local upload store for remote attachment ingest
Host-owned attachment handles for remote session input
chunked attachment ingest for large uploads
media.read
media.download
media.stream
chunked E2EE media frames
media.stream offset resume
media.stream resume_token reconnect resume
media.stream.cancel
event_seq + media_id reference validation
E2EE response frames
Host path never exposed in control response
```

Local Desktop 可以继续用本地 HTTP URL 渲染媒体，但 RemoteCoreClient 和 Mobile 必须只依赖 capability 和 encrypted media frames。

### Phase 8 - Workspace Gateway

把 workspace 文件和命令能力收口到 Host Gateway。Controller 只能请求 Host 操作 workspace root 内的路径，不能把 Controller 本机路径当作 Host 路径。

```text
已落地:
workspace.files.read
workspace.files.write
workspace.files.apply_patch
workspace.files.delete
workspace.files.move
workspace.files.stream
workspace.files.stream.cancel
workspace.exec
workspace.exec approval policy gate
local workspace root confinement
SSH workspace Host-initiated read/range-read/write/delete/move/exec
E2EE request/response frames
chunked E2EE workspace_file frames
no Host local absolute root in workspace.files.read response

待落地:
async workspace.exec approval interaction, if product needs per-command confirmation UI
```

`workspace.files.read` v1 用于目录列表和中小文件读取；文件内容以 base64 放在 encrypted control response 中。`workspace.files.write` action v1 只创建或覆盖单个文件；精确编辑、删除、移动和大文件读取分别使用独立 action，避免把文件管理语义塞进一个过宽 action。

`workspace.files.apply_patch` v1 是 Host 侧精确文本编辑能力：Controller 提交 `old_string`/`new_string` edits，Host 在 workspace root 内读取目标文件，要求默认单次匹配唯一；只有显式 `replace_all` 才允许替换多处。它不解析完整 unified diff，不 shell out 到 `patch`，也不做跨端文件同步。这样先满足远控编辑需要，同时把 delete/move/大文件流式读写作为独立 action 后续扩展，避免把文件管理语义一次性塞进一个过宽 action。

`workspace.files.delete` 和 `workspace.files.move` v1 是独立 Host 侧文件管理 action，复用 `workspace.files.write` capability。它们只接受 workspace root 内的路径，不能删除或移动 workspace root 本身；删除目录必须显式 `recursive=true`；移动默认不覆盖已有目标，只有显式 `overwrite=true` 才允许覆盖非目录目标。SSH workspace 中由 Host 通过 proxy helper 发起 `remove`/`move`，Controller 不接触远端路径之外的本机文件系统能力。

`workspace.files.stream` v1 用于大文件读取：普通 `workspace.files.read` 仍负责目录列表和中小文件 base64 response；超过普通 response 适合承载的内容由 Controller 请求 `workspace.files.stream`，Host 返回 stream metadata 后在同一 encrypted control connection 上发送 `workspace_file.chunk` / `workspace_file.completed` / `workspace_file.error` frame。Controller 可以通过 offset 重新请求来恢复读取，也可以用 `workspace.files.stream.cancel` 取消当前 control connection 上的 active stream。SSH workspace 通过 proxy helper 的 `read_range` 从远端按 chunk 读取，Host 只转发密文 frame，不生成可被 relay 或云端读取的明文 URL。

`workspace.exec` v1 由 Host trust grant 的 `workspace_exec_policy` 决定是否执行。默认 `trusted` 表示 `workspace.exec` capability 本身就是执行授权；`require_approval` 会同步拒绝执行并返回 `workspace_exec_approval_required`，不会启动本地或 SSH command；`disabled` 直接拒绝为 `workspace_exec_disabled`。这先把 command approval policy 放进 Core/daemon 决策层，避免 Controller 自行判断或把 gateway command 伪装成 Claude/Codex 原生 approval。若未来需要逐条命令确认，应该在这个 policy gate 之上新增 Host-owned async interaction，而不是让客户端绕过策略。

### Phase 9 - PTY Attach Manager

把当前“一条 WebSocket 对应一个 PTY”的语义升级成 Host-owned terminal session：

```text
已落地:
terminal.open
terminal.attach
terminal.detach
terminal.input
terminal.resize
terminal.close
terminal.output stream
single active writer
multi viewer
retention timeout
trust revocation releases active writer lock
lifecycle event only, no ANSI output JSONL storage

待落地:
跨进程持久 terminal session 恢复
```

### Phase 10 - Mobile Controller

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
登录云账号后自动信任新设备
让 Controller 设备直接访问 SSH key 或 Host 文件
把 PTY 字节输出塞进 AstralEvent JSONL
把附件或媒体明文上传到云端/relay
把 Host 本地 path 当作远程 Controller 可访问资源
把 desktop 本机设置和自动更新默认暴露成远控能力
把 LAN discovery 结果当作信任来源
在 v1 做 NAT punching、STUN/TURN、WebRTC 或复杂 transport migration
```

## 核心不变量

AstralOps 必须始终保持这个不变量：

```text
Cloud 是账号入口和 mesh 路由器。
Relay 是不透明 packet forwarder。
Desktop Host 是执行权威。
Controller 设备是完整远程 UI。
业务 payload 在 Controller 和 Host 之间端到端加密。
附件、媒体、PTY 都是 Host-owned resource stream，不是云端同步数据。
LAN 和 relay 只是传输路径；E2EE 和 Host trust store 才是安全边界。
```
