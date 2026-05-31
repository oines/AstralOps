# AstralOps 远程控制 Mesh 架构

最后更新：2026-05-30

## 总览

AstralOps 未来不是“云端同步工作区数据”的系统，而是一个由云账号托管连接的、端到端加密的多设备远程控制 Mesh。

核心模型是：

```text
账号
  -> 多台设备
  -> 多组可信控制关系
  -> Controller 与 Desktop Host 之间建立加密点对点控制会话
```

云服务和任意 relay 节点只负责账号、设备发现、在线状态、配对授权、信令和必要时的流量中继。Cloud 是账号控制面，Relay 是不透明数据转发面；两者可以在 MVP 中同进程部署，但客户端和协议边界必须分开。它们不能查看 prompt、session、workspace、event payload、approval 内容、PTY 输出、SSH 信息或文件树。

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

## Cloud / Relay 分层

AstralOps v1 不做 per-device relay 调度，也不做 relay-to-relay 转发。为了控制复杂度，一个账号只选择一个默认 relay：

```text
Account
  -> default relay_id
  -> default relay_url
  -> all devices in this account use that relay for non-LAN fallback
```

设备登录或同步 cloud 时先调用 `GET /v1/account` 获取账号默认 relay。随后：

```text
CloudClient:
  GET /v1/account
  GET /v1/relays
  PATCH /v1/account/relay
  GET/POST /v1/devices
  pairing requests

RelayClient:
  GET /v1/relay/connect (WebSocket)
  GET/POST /v1/relay/envelopes (legacy/test HTTP queue)
  POST /v1/relay/envelopes/:id/ack (legacy/test HTTP queue)
```

客户端不能把 `cloud.base_url` 直接当 relay URL 使用，也不能把 cloud account token 发给 relay。它必须从账号响应的 `relay.relay_url` 和 `relay.credential` 构造 `RelayClient`。Cloud control plane 和 relay 是部署边界不同的服务：cloud 负责账号、设备 registry、presence、pairing signal、吊销、账号 relay catalog、账号当前 relay 配置和短期 relay credential 签发；relay 只负责校验 credential 并投递 opaque envelope。开发测试可以临时把二者部署在同一台机器，但 daemon 代码必须继续通过 `CloudClient` / `RelayClient` 分离调用。

`GET /v1/account` 的 relay 字段：

```json
{
  "relay": {
    "relay_id": "vps-default",
    "relay_url": "https://us-relay-astralops.oines.dev",
    "credential": "<opaque-short-lived-relay-credential>",
    "credential_expires_at": "2026-05-30T10:00:00Z"
  }
}
```

Relay credential 是 cloud 用与 relay 共享的 HMAC secret 签出的短期凭据，不是账号 token。Relay 本地验签，不查 cloud 数据库，不知道 cloud account token。credential payload 至少包含：

```text
version = astralops-relay-credential-v1
alg = HS256
kid
relay_id
account_id_hash
iat
exp
```

Relay 校验规则：

```text
Authorization: Bearer <relay-credential>
签名必须匹配 kid 对应 secret
relay_id 必须匹配当前 relay
account_id_hash 作为队列 namespace
exp 必须未过期
exp - iat 不能超过 relay 配置的最大 TTL
```

多地区支持方式是按账号选择 relay：

```text
账号当前 relay -> cn-nanjing / us / ...
同账号所有设备 -> 使用同一个当前 relay
```

同一个账号内所有设备默认使用同一个 relay。这样 A 找 B 时只需要：

```text
A 登录 cloud
A 读取账号 relay
A 从 cloud device registry 找 B 的 public metadata / presence
A 通过账号 relay 的 WebSocket 转发 opaque envelope 给 B
```

如果某台设备还拿着旧 relay 配置，v1 不做跨 relay 查找或转发；它需要等下一次 cloud sync 读取新的账号 relay 后再参与 relay fallback。LAN 直连不受这个限制，仍然优先使用。

MVP 不做 previous relay grace、双 relay 投递、relay-to-relay 转发或跨 relay 查找。用户在设置里切换账号 relay 时，daemon 只调用本机 daemon 的 `PATCH /v1/cloud/account/relay`，daemon 再调用 Cloud 的 `PATCH /v1/account/relay`。Cloud 持久化账号当前 `relay_id` 后立即对后续 `GET /v1/account` 签发新 relay credential。其他设备在下一次 Cloud sync 后切到新 relay；切换窗口内 relay fallback 可能短暂不可用，但不会破坏 LAN 直连和设备 E2EE 信任边界。

默认 relay 传输是 WebSocket：Host daemon 登录 Mesh 后连接账号 relay，并用自己的 `device_id` 挂在线转发通道；Controller 需要控制 Host 时也连接同一个账号 relay，发送 `control.hello`，随后同一条 WebSocket 承载 `control.hello_ack` 与 `control.sealed_frame`。Relay 不持久化 WebSocket 消息，也不解析 payload。HTTP envelope queue 仍保留为旧接口和测试夹具，不作为 daemon 的默认交互路径。

开发默认账号 relay 是 `cn-nanjing`：

```text
relay_id = cn-nanjing
relay_url = http://119.45.166.88:43911
```

## Cloud Control Plane

生产 cloud control plane 是 AstralOps 的商业控制面，不放在公开仓库。公开仓库只保留客户端协议、daemon 调用边界、relay 客户端和测试专用 fake broker；正式 cloud 实现放在私有仓库 `oines/AstralOps-Cloud`。

```text
公开仓库:
  desktop / daemon / proxy-agent / protocol / relay-facing client contracts

私有仓库:
  production cloud account service
  OAuth provider integration
  device registry / presence / pairing signal / revocation store
  account relay configuration
  production database migrations / deployment / operations config
```

当前内测 cloud 可以继续使用 token allowlist 作为临时账号命名空间，但真实 token、OAuth client secrets、数据库 URL、生产 config、VPS 凭据和迁移脚本不能提交到公开仓库。

私有 cloud 的内测配置示例：

```text
ASTRALOPS_CLOUD_ACCOUNT_TOKENS=<long-random-token-1>,<long-random-token-2>
```

账号默认 relay 由 cloud 配置下发：

```text
ASTRALOPS_ACCOUNT_RELAY_ID=default
ASTRALOPS_ACCOUNT_RELAY_URL=https://us-relay-astralops.oines.dev
```

公网部署时 cloud 必须拒绝未配置账号/OAuth 凭据的 open mode。临时账号 token 长度必须至少 32 字符，推荐使用 32 bytes 以上随机值。Cloud 还必须启用 HTTP read/write/header timeout，并限制单个 JSON 请求体大小，避免公网开发节点被简单慢请求或超大请求拖垮。

客户端使用：

```text
Authorization: Bearer <account-token>
```

内测 token 模式下 cloud 不存 token 明文，只用 token 派生 `account_id_hash`。同一个 token 对应同一个账号 namespace。这个方案只用于开发和小规模内测，不是最终消费者账号体验。

正式账号 MVP 由私有 cloud control plane 承担 OAuth broker 职责，Desktop daemon 不持有 Google/GitHub OAuth client secret：

```text
Desktop daemon
  <- local UI POST /v1/cloud/auth/start
  -> build localhost callback + one-time desktop state
  -> return cloud /v1/auth/<provider>/start URL to local UI
  -> browser OAuth
  -> Cloud callback
  -> redirect localhost /v1/cloud/auth/callback with login_code + desktop state
  -> POST /v1/auth/login-code/exchange with current public device identity
  -> receive device-bound AstralOps account session token
  -> save cloud settings locally and start cloud device sync
```

Cloud 可支持：

```text
GET  /v1/auth/google/start
GET  /v1/auth/google/callback
GET  /v1/auth/github/start
GET  /v1/auth/github/callback
POST /v1/auth/login-code/exchange
```

不管用户用 Google、GitHub 或后续邮箱/passkey 登录，Cloud 内部都必须映射到 AstralOps 自己的 `account_id`，对客户端只暴露 `account_id_hash`。OAuth state、login code、account session token 都必须按一次性/过期语义处理；login code 和 account session token 不得明文落盘，只能保存 hash。正式 OAuth 登录换取的 account session token 必须绑定发起登录时提交的 `device_id` 和 `public_key_fingerprint`，不能作为账号级万能 token 使用。换到正式账号后，外部设备 API 仍保持账号下设备注册、presence、pairing signal、账号 relay 配置、relay envelope 这几个边界。

`POST /v1/auth/login-code/exchange` 请求体：

```json
{
  "login_code": "<one-time-login-code>",
  "device": {
    "device_id": "dev_...",
    "device_name": "oinesdeMacBook-Air.local",
    "device_kind": "desktop",
    "public_key": "<base64-ed25519-public-key>",
    "public_key_fingerprint": "sha256:...",
    "capabilities": ["core.read", "terminal.open"],
    "can_host": true,
    "can_control": true
  }
}
```

Cloud 在兑换 login code 时原子注册/更新这台设备，并把返回的 session token 绑定到这台设备。绑定后的 token 只能注册、heartbeat、offline 当前 device identity；不能拿同一个 token 注册另一个 `device_id`。删除某台设备时，Cloud 必须同时撤销该设备绑定的 sessions。这样“从 Mesh 删除设备”才是从账号 Mesh 踢出这台设备，而不是只隐藏一个旧 device record。

私有 cloud MVP 可以先使用 VPS 本地 JSON 文件持久化：

```text
/var/lib/astralops-cloud/cloud.json
```

这适合少量内测设备，优点是部署成本低、没有数据库运维；缺点是单节点、并发能力有限、没有高可用。迁移到 Postgres 时，只替换私有 cloud store 层，不改变 Desktop daemon、Mobile Controller、Host trust store 或 E2EE control channel。

Cloud service API 的职责：

```text
GET  /v1/health
GET  /v1/auth/google/start
GET  /v1/auth/google/callback
GET  /v1/auth/github/start
GET  /v1/auth/github/callback
POST /v1/auth/login-code/exchange
GET  /v1/account
GET  /v1/devices
POST /v1/devices
POST /v1/devices/:device_id/heartbeat
POST /v1/devices/:device_id/offline
POST /v1/devices/:device_id/remove
GET  /v1/pairing/requests?device_id=<device_id>
POST /v1/pairing/requests
GET  /v1/pairing/requests/:request_id
POST /v1/pairing/requests/:request_id/resolve
```

`GET /v1/account` 返回账号公共配置：

```json
{
  "account_id_hash": "acct_...",
  "membership_key_id": "default",
  "membership_signing_public_key": "<base64-ed25519-public-key>",
  "relay": {
    "relay_id": "default",
    "relay_url": "https://us-relay-astralops.oines.dev"
  }
}
```

Relay API 的职责：

```text
GET  /v1/relay/connect?device_id=<device_id>  (WebSocket)
GET  /v1/relay/envelopes?device_id=<device_id>&wait=10s  (legacy/test)
POST /v1/relay/envelopes  (legacy/test)
POST /v1/relay/envelopes/:envelope_id/ack  (legacy/test)
```

Relay API 必须由独立 relay service 提供，不能由 cloud control plane 顺手挂载 `/v1/relay/*`。daemon 代码必须通过独立的 `RelayClient` 调用这些 endpoint。
`/v1/relay/connect` 使用同一个短期 relay credential 鉴权。连接建立后客户端发送 `{"type":"send","envelope":{...}}`，relay 向目标设备当前在线 WebSocket 推送 `{"type":"envelope","envelope":{...}}`。这条通道只转发 opaque envelope，不做业务确认、不保存明文、不跨 relay 转发。旧 HTTP queue 的 `wait` 是长轮询参数，保留给测试和兼容，不是默认 daemon 传输路径。

公开仓库提供独立 relay 进程：

```text
ASTRALOPS_RELAY_ID=vps-default \
ASTRALOPS_RELAY_CREDENTIAL_SECRETS=vps-1:<long-random-secret> \
go run ./relay --addr 0.0.0.0:43911
```

Cloud control plane 使用同一组 relay credential secret 签发 credential：

```text
ASTRALOPS_ACCOUNT_RELAY_ID=vps-default
ASTRALOPS_ACCOUNT_RELAY_URL=https://us-relay-astralops.oines.dev
ASTRALOPS_RELAY_CREDENTIAL_KID=vps-1
ASTRALOPS_RELAY_CREDENTIAL_SECRETS=vps-1:<long-random-secret>
ASTRALOPS_RELAY_CREDENTIAL_TTL=10m
ASTRALOPS_MEMBERSHIP_SIGNING_KID=default
ASTRALOPS_MEMBERSHIP_SIGNING_KEY=<base64-ed25519-private-key>
ASTRALOPS_MEMBERSHIP_LEASE_TTL=24h
```

Cloud 对 register/heartbeat 返回短期 `membership_lease`，由 Cloud membership signing key 签名。lease payload 只包含账号 hash、device_id、public_key_fingerprint、can_host/can_control、mesh_epoch、iat/exp。客户端把当前设备 lease 持久化在本机私有 daemon 数据里；lease 只证明“这个 device identity 当前仍属于账号 Mesh 且具备 host/control 角色”，不授予 Host trust。

公网 relay 不能依赖 cloud 进程内状态，不能读取 cloud 数据库，也不能接受 cloud account token。它只能基于短期 relay credential 得到账号 namespace，并把 opaque envelope 转发给同账号 namespace 下的目标 `device_id` WebSocket。

Desktop daemon 通过本机 authenticated API 接入 cloud service。这个 API 只读写本机 daemon settings 并调用 cloud，不暴露给远程 Host listener：

```text
PATCH /v1/settings
  cloud.enabled=true
  cloud.base_url=https://cloud-astralops.oines.dev
  cloud.account_token=<account-token>

POST /v1/cloud/auth/start
GET  /v1/cloud/auth/callback
POST /v1/cloud/auth/logout
GET  /v1/cloud/devices
POST /v1/cloud/devices
POST /v1/cloud/heartbeat
GET  /v1/cloud/pairing/requests?device_id=<device_id>
POST /v1/cloud/pairing/requests
POST /v1/cloud/pairing/requests/:request_id/resolve
```

`POST /v1/cloud/auth/start` 只能由本机 authenticated Desktop UI 调用。daemon 生成高熵一次性 state 和 `http://127.0.0.1:<daemon-port>/v1/cloud/auth/callback`，返回 Cloud OAuth start URL；Desktop 只负责用系统浏览器打开这个 URL。

`GET /v1/cloud/auth/callback` 是给系统浏览器回跳的本机入口，不要求 daemon auth token，但必须校验 daemon 内存中的一次性 state。state 过期、重复使用或不匹配时只能显示失败页，不能兑换 login code。兑换 login code 时 daemon 必须提交当前 public device identity，让 Cloud 返回绑定这台设备的 account session token。兑换成功后 daemon 保存 `cloud.base_url` 和 `cloud.account_token` 到本机 settings，并触发 cloud sync。renderer 不能直接接触 OAuth client secret、login code exchange 细节或 relay credential 本体。

`POST /v1/cloud/auth/logout` 是本机退出当前账号 Mesh 的身份边界。daemon 会先 best-effort 调 cloud `remove` 撤销当前 device id，再关闭 cloud sync/relay、断开远控连接、清理 account token，并重置本机 mesh device identity、Host trust grants、known hosts 和 pairing requests。本地 workspace、session、event、文件和 SSH 配置不属于 Mesh 生命周期，退出账号不会删除这些数据。即使 cloud remove 因网络失败没有完成，本机也必须先退出 Mesh；旧 cloud presence 依赖 cloud 端 TTL 变成 offline/不可连。

`/v1/cloud/devices` 注册的是当前 daemon 的 public device identity。默认 `can_control=true`，`can_host` 取本机 `remote_control.enabled`，调用方也可以在注册请求里显式覆盖。这个动作不会自动开启 Host listener；是否允许被远控仍然由本机 `remote_control` settings 决定。

daemon 启动后如果 `cloud.enabled=true`，会先读取 `GET /v1/account` 的账号 relay 配置和 membership signing public key，再向 cloud 注册当前设备并定时 heartbeat。注册和 heartbeat 中的 `relay_url` 只是 presence/routing metadata，来自账号默认 relay，不是每台设备任意选择的 relay。register/heartbeat 必须返回当前设备 `membership_lease`；daemon 验签通过后才认为本机是 active Cloud Mesh 成员。关闭 cloud settings 时，daemon 会尝试把当前设备标记为 offline；正式退出登录则走上面的 Mesh logout/reset 语义。自动同步只发送 public device identity、capabilities、can_host/can_control、presence 和账号 relay routing metadata，不发送工作区、session、事件、SSH 或路径数据。

如果本机 `remote_control.enabled=true`，daemon 的 cloud sync 还会拉取 `host_device_id == 本机 device_id` 的 pending pairing request。这个同步只能把云端信令转换成 Host 本地 `PairingRequest(source=cloud, cloud_request_id=...)`，并通过 cloud device registry 取得 Controller 的 public key；它不能直接写入 `TrustGrant`。Host UI 或已有可信 `host.manage` Controller 批准/拒绝本地 request 后，Host 再把同一个 `cloud_request_id` 回写为 approved/denied。Cloud 必须校验 resolve 请求来自该 pairing request 的 Host 设备绑定 session，`resolver_device_id` 不能由任意同账号设备伪造。云端状态只是信令状态，真正可控条件仍然是 Host 本地 trust store、E2EE 握手和 capability 校验。

`GET /v1/remote/hosts` 只在本机已登录 Cloud、当前 device 未被 revoked 且本机 membership lease 仍有效时返回远端 Host。列表来源是同账号 cloud devices 中 `can_host=true` 的设备；`known_hosts.json` 只是 Controller 侧 Host identity cache，不能单独把 stale 设备放回 selector。LAN discovery 只能把已经属于 cloud Mesh 列表且 known host fingerprint 匹配的设备升级成 `lan` 路由。Cloud 候选只代表“账号下可发现的 Host 设备”，不代表已经获得控制权。真正发起远控 action 前仍必须满足：

```text
本机 Controller 已登录 Cloud Mesh
目标 Host 已登录 Cloud Mesh
双方 Cloud membership lease 都有效且角色匹配
本地已知 Host identity / known host 匹配
Host 本地 trust grant 存在且未 revoked
E2EE control channel 握手成功
capability 校验通过
```

这些 API 只能处理云端允许的数据：

```text
device_id / device_name / device_kind
public_key / public_key_fingerprint
can_host / can_control / capabilities
online/offline presence / last_seen
pairing request metadata
opaque relay envelope
```

这些 API 不能增加字段来保存：

```text
workspace/session/event payload
prompt / assistant output
approval / ask / plan 内容
PTY 输出
文件树或文件内容
SSH 配置
本地路径
私钥
```

`POST /v1/pairing/requests/:request_id/resolve` 只是云端信令状态同步，不是授予 Host 控制权。真正授信仍必须发生在目标 Desktop Host 本地：

```text
Host UI 或已有可信 host.manage Controller 批准
  -> Host 本地写入 TrustGrant
  -> Host 可通知 cloud service 标记 pairing request approved
```

任何 Controller 都不能因为 cloud service 上的 request 状态是 `approved` 就绕过 Host 本地 trust store。连接和每个 action 仍必须由 Host 校验：

```text
controller_device_id trusted
grant 未 revoked
capability 允许
E2EE session 有效
```

Relay envelope 只允许：

```json
{
  "version": "astralops-relay-envelope-v1",
  "connection_id": "ctrl_...",
  "from_device_id": "dev_controller",
  "to_device_id": "dev_host",
  "payload_kind": "control.hello | control.hello_ack | control.sealed_frame",
  "payload_base64": "..."
}
```

`control.hello` 和 `control.hello_ack` 是现有设备级 E2EE 握手帧的 relay 投递形态，用于在没有 LAN 直连时完成同一套签名校验和会话密钥派生。握手完成后，业务 `request/response/stream` 只能放进 `control.sealed_frame`。cloud/relay service 只能转发 opaque envelope 并按账号/设备做路由和限流，不能解析业务协议。

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

控制握手还必须绑定 Cloud membership lease。Controller 在 `control.hello` 中带自己的 lease；Host 校验该 lease 由当前账号 Cloud membership public key 签发，payload 中的 `device_id` 和 `public_key_fingerprint` 必须匹配 hello 中的 controller identity，且 `can_control=true`。Host 在 `control.hello_ack` 中带自己的 lease；Controller 用本地保存的账号 signing public key 校验 Host identity 和 `can_host=true`。两侧 lease 字段都进入 Ed25519 握手签名 payload，防止 relay/LAN 中间人替换、剥离或跨连接复用 lease。

控制会话必须把防重放作为协议层约束，而不是依赖 relay 投递语义。握手后派生两把方向密钥：`controller-to-host` 和 `host-to-controller`。每个 `sealed` frame 的 AES-GCM AAD 必须绑定 `protocol_version`、`connection_id`、方向和严格连续的 `seq`。接收端只接受 `seq == previous_seq + 1`；重复、乱序、跳号或跨方向复用的 frame 都必须视为非法 frame 并关闭 control session。LAN direct 和 relay transport 复用同一套 sealed frame 校验。

推送通知不能包含 session 或任务内容。推送最多只能提示 AstralOps 有新活动；App 打开后必须通过 E2EE channel 拉取详情。

## 远控传输策略

远控 v1 使用 LAN-first relay fallback。传输路径不参与信任判断：

```text
Transport is not trust.
LAN direct 和 relay 只是 packet path。
所有路径都必须跑同一套设备认证 E2EE 握手。
```

客户端建连必须分成三层：

```text
RemoteTargetResolver
  -> 解析目标 Host identity、known host、LAN candidate、账号 relay

ControlTransport
  -> direct LAN
  -> explicit direct host fallback
  -> relay
  -> future p2p

ControlChannel
  -> E2EE frame
  -> request/response
  -> stream / PTY / attachment / media
```

`ControlTransport` 只负责打开一条可以读写 encrypted control frame 的连接。workspace、session、PTY、file、media、approval、trust 等业务能力不能分叉成 LAN 专用逻辑或 relay 专用逻辑。未来加入应用层 P2P 时，只能新增一个 `p2p` transport，并把它插入 transport plan：

```text
LAN direct
P2P direct
relay fallback
```

P2P 失败不能改变 Host identity 校验、Host trust grant、E2EE 握手或 capability 校验。

Desktop app 的远端 Host 操作入口和开发 CLI 都必须复用同一个 `RemoteTargetResolver` 边界。UI handler 不能复制 LAN discovery、cloud registry、known host、account relay 的解析分支；它只能请求 resolver 返回目标 Host identity 和可用 transport plan。

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

当前 daemon 通过本机设置开启 LAN listener：

```text
remote_control.enabled=true
remote_control.listen_addr=0.0.0.0:43900
remote_control.lan_discovery=true
```

开启 LAN listener 时，daemon 默认开启 UDP discovery。Desktop 设置页必须只写入本机 daemon settings；云端不能替用户默认开启 Host。开发或排障时可以关闭 discovery：

```text
remote_control.lan_discovery=false
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

当前开发客户端在 `pair` 或 `pair-request` 成功后，会把 Host public identity 记入本机 `known_hosts.json`。这只是 Controller 侧的 Host 身份缓存，用来校验后续 LAN discovery candidate；真正的执行授权仍然由 Host 本地 trust store 决定。`pair` 是 dev-only trust 直写；`pair-request` 是正式流程的最小本地 smoke，会在 Host 上创建 pending request，等待本机 Host UI 或已有可信 `host.manage` Controller 批准。

```text
go run ./daemon control-client known-hosts
go run ./daemon control-client pair-request --host http://<host>:43900
go run ./daemon control-client pair-status --host http://<host>:43900 --request-id <request_id>
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
go run ./daemon control-client smoke --relay --relay-timeout 60s --cloud-base-url <broker_url> --cloud-token <account_token> --host-device-id <host_device_id>
go run ./daemon control-client smoke --relay --relay-timeout 60s --cloud-base-url <broker_url> --cloud-token <account_token> --host-device-id <host_device_id> --workspace-id <workspace_id> --path . --workspace-write-smoke --exec-command "pwd" --sessions --events --trust-list
go run ./daemon control-client smoke --relay --relay-timeout 60s --cloud-base-url <broker_url> --cloud-token <account_token> --host-device-id <host_device_id> --workspace-id <workspace_id> --session-id <session_id> --event-subscription --stream-path large.log --terminal
```

开发客户端可以同时传 `--discover --host <relay_or_host_url>`。这种模式会先按 `host_device_id` 尝试已知 Host 的 LAN candidate；LAN discovery、短超时 Host identity validation 或 control channel dial/handshake 失败时，回退到显式 `--host`。`--host` 在当前 MVP 里可以是手填远控 URL，后续也可以是 relay URL；fallback 不改变 E2EE 握手和 Host trust store 校验，并且 fallback Host identity 必须匹配本地 known host 中的目标 `device_id + public_key`。没有 known host 身份时不能使用 fallback。

`control-client smoke` 只走设备间 E2EE control channel，不访问本地 workspace/session/settings/events HTTP API。默认只验证 `core.read.workspaces`；可选 `--sessions` 验证 `core.read.sessions`，可选 `--session-view` 验证指定 `--session-id` 的 `core.read.session_view`，可选 `--events` 验证 `core.read.events` 的窗口读取，可选 `--event-subscription` 验证 `core.subscribe.events` replay event frame；两者都只走 E2EE control channel。可选 `--trust-list` 验证 `host.trust.list` 的非破坏性 Host management 查询；传入 `--workspace-id` 后会验证 `workspace.files.read`，可选 `--stream-path` 验证 `workspace.files.stream` 的 chunked E2EE frames，可选 `--workspace-write-smoke` 在 Host workspace 临时目录中验证 `workspace.files.write/apply_patch/move/delete` 并清理，可选 `--exec-command` 验证 `workspace.exec`，可选 `--terminal` 验证 Host-owned PTY 的 `terminal.open/attach/input/output/close` E2EE 链路。传入 `--session-id --attachment-path` 后会验证 `attachment.ingest.start/chunk/finish`，Controller 文件会通过 E2EE channel 分片上传到 Host-owned attachment store。传入 `--session-id --media-event-seq --media-id` 后会验证 transcript media reference 的 `media.stream` E2EE frames，并返回 resume_token/bytes/chunks 等摘要。stream/attachment/media/workspace-write/terminal/trust/session/events/event-subscription smoke 只输出摘要，不打印文件内容、事件正文、PTY 输出内容或 Host path。

`control-client smoke --relay` 强制跳过 LAN/direct transport，只通过账号默认 relay 的 opaque relay envelope 做控制通道握手和 E2EE control frame 投递。它必须提供 `--cloud-base-url`、`--cloud-token` 和本机 known host 中已有的 `--host-device-id`；执行前会先从 `GET /v1/account` 读取 `relay.relay_url`，再用 cloud device registry 校验目标 Host 在线、`can_host=true`，并且云端 public key/fingerprint 与本机 known host 匹配。relay transport 复用同一套 request/response 和 stream frame 语义，覆盖 `core.read.*`、`workspace.files.read/write/apply_patch/move/delete/stream`、`workspace.exec`、`core.subscribe.events`、`attachment.ingest.*`、`media.stream`、Host-owned PTY 和 `host.trust.*`。云端和 relay 节点仍然只看到 account/device public metadata、presence/routing metadata、trust/revocation metadata 和 opaque sealed envelopes，不接收 workspace/session/event payload、文件内容、PTY 输出、SSH 配置、附件或媒体内容。

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

LAN identity validation 成功只说明候选地址属于目标 Host，不能把目标降级成 direct-only。只要账号 relay 可用，`RemoteTargetResolver` 返回的 LAN target 也必须携带 relay fallback；后续 control websocket dial、hello/ack 或 request/response 超时都可以切到 relay，但不能绕过同一个 Host identity、trust grant、capability 和 E2EE 校验。Transport 打开超时和业务 action 响应超时必须分开：LAN dial/handshake 可以短超时，`core.control.workspace.connect` 这类 Host-owned SSH 连接操作必须使用足够的 action timeout，不能因为 LAN transport 是 2 秒窗口就提前判失败。

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
PTY 支持多个 viewer，也支持多个可信 Controller shared input。
Host 按收到 input frame 的顺序写入 PTY，不维护写入锁。
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
7. 批准后，Host 写入 device-to-device trust grant，并把 pairing request 标记为 approved。
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

当前 Desktop MVP 的 Host selector 会展示本机和云端发现的 Desktop Host。`GET /v1/remote/hosts` 必须用 `known_identity` 显式告诉 UI 该 Host 是否已经写入本机 known host，Desktop 不能用 URL 字段猜测信任状态。对于只有 cloud registry 记录、但本机还没有 known host identity 的 Host，主页只能显示“请求控制”入口，不能加载 workspace/session，也不能假装已经可控。点击请求控制会通过本机 daemon 向 cloud service 创建 pairing request；目标 Host 同步到本地 pending request 后，必须在远控设置页或已有可信 `host.manage` Controller 中批准/拒绝。批准前 Controller 端仍不能进入 Host workspace/session。批准后，Controller daemon 可以把 cloud registry 中目标 Host 的 public identity 写入本机 `known_hosts.json`，使 Host selector 进入可连接状态；这只是 Host identity cache，不是信任授权，后续握手和每个 action 仍必须由目标 Host 本地 trust grant 校验。

控制授权必须按状态流转实现，不能靠新增一次性“重新授权”接口绕过 pairing/trust 模型：

```text
none / needs_pairing
  -> pending        Controller 请求控制，等待目标 Host 批准
  -> trusted        目标 Host 批准，写入本地 trust grant
  -> revoked        目标 Host 撤销控制权，关闭 active control sessions
  -> pending        同一 Controller 再次请求控制，重新等待目标 Host 批准
  -> trusted        目标 Host 再次批准，revoked grant 被重新置为 trusted
```

`known_identity` 只表示 Controller 认识目标 Host 的 public identity；`authorization_state` 才表达当前控制入口状态。若 Controller 已有 known host 但握手收到 Host 的 `capability_denied` / `control_authorization_required`，Core 必须把它归一成“控制权已撤销/需要重新请求控制”，UI 只能显示重新进入 `pending` 的请求入口，不能继续加载远端 workspace/session，也不能把 `invalid hello_ack` 这类低层错误直接暴露给用户。重新请求控制仍复用同一个 cloud pairing signal 流程；批准前没有任何远控能力恢复。

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

Desktop 远控设置页必须从本机 daemon 读取账号状态，而不是让 renderer 直接拼 cloud/relay 请求。UI 可展示：

```text
Cloud 连接状态
account_id_hash
账号默认 relay_id / relay_url
可选 relay 列表和当前账号 relay
relay credential 过期时间
账号设备列表
本机 Host trust grants
pending pairing requests
```

本机 daemon 暴露给 Desktop UI 的账号状态不能返回 relay credential 本体；只返回是否已下发和过期时间。renderer 也不能把 cloud account token 发给 relay。

用户可以：

```text
重命名本机设备
允许/关闭本机被远控
查看可信设备
撤销某个 Controller 对本机的访问
从账号 Mesh 移除设备
查看连接路径：LAN / relay
```

账号 Mesh 的设备移除是 cloud registry 层动作：

```text
POST /v1/devices/:device_id/remove
  -> device.status = revoked
  -> relay_url 清空
  -> 相关 pending pairing request 标记 denied
  -> 同一个 device_id 后续 register/heartbeat/pairing 被拒绝
```

它不会替代 Host 本地 trust revoke。mesh 注册状态只决定“账号里是否还能发现和中继这个设备”，真正能不能控制某台 Host 仍由那台 Host 本地 trust store 和当前 E2EE control session 决定。如果要让正在控制某台 Host 的设备立即断开，仍必须对目标 Host 执行 `POST /v1/trust/devices/:device_id/revoke` 或等价的 `host.trust.revoke` 控制动作。

如果移除的是当前本机 device id，本机 daemon 必须把它当作 `cloud auth logout` 处理：从账号 Mesh 删除旧 device id，并立即执行本地 mesh identity reset。下一次登录同一个账号时，本机会以新的 device id 重新加入 Mesh，并需要重新建立 Host trust。

Desktop 设置页可以在用户从账号 Mesh 移除设备时请求本机 daemon 一并撤销本机 trust：

```json
{
  "revoke_local_trust": true
}
```

这个合并操作仍然是两个边界清晰的动作：cloud registry 负责把设备从账号 Mesh 移除，本机 Host/Core 负责撤销本机 trust、关闭 active control sessions、释放 PTY writer。renderer 只表达用户意图，不能自己把 cloud remove 和 trust revoke 拼成业务状态。

为了覆盖“设备被移除时目标 Host 离线”的情况，每台设备上线并执行 cloud sync 时必须把云端 `revoked` 设备投影到本地：

```text
cloud device.status == revoked
  -> 如果本机 Host trust store 信任过该 device_id，本机立即 revoke trust grant
  -> 关闭该 device_id 的 active control session / relay session
  -> 释放该 device_id 持有的 PTY writer
  -> 标记本机 known_hosts 中该 device_id 为 revoked，阻止后续 LAN 直连
  -> 本地 pending pairing request 标记 denied
```

如果当前设备自身已经在云端被标记为 `revoked`，daemon 必须停止把它当作账号 Mesh 成员继续发起或接受远控，关闭现有 control session，并执行本地 mesh logout/reset；即使目标 Host 仍在 LAN 内，也不能因为本地 known host 缓存而继续走 LAN 控制。

如果 Cloud 暂时不可达，设备不做每次握手实时 Cloud 查询；MVP 采用短期 membership lease 控制离线窗口。Host 接受 LAN/relay `control.hello` 前必须校验 Controller lease 签名、账号、device_id、public key fingerprint、`can_control` 和过期时间；Controller 接受 `control.hello_ack` 前必须校验 Host lease 的 `can_host`。lease 过期后，即使本地 trust grant 还在，也不能继续新建远控连接。默认 TTL 为 24h，可通过 Cloud 部署配置收紧；这比每次握手查 Cloud 简单、可维护，并且避免被移除设备在永久离线状态下无限期继续 LAN 控制。

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
  -> Host B 移除 A 的 PTY viewer/input 权限；PTY 本身继续归 Host 所有
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
5. 取消该 control connection 绑定的 Host-owned action context，包括 event/media/workspace file streams 和正在执行的 workspace.exec。
6. 清理该设备的 event subscription、pending request、PTY attach。
7. 不关闭 Host-owned PTY；其他仍可信且具备 `terminal.input` 能力的 Controller 可以继续 shared input。
8. append 本地 audit event，例如 control.trust.revoked。
9. 通知云端同步 trust metadata。
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

## Desktop 主界面模型

Desktop 主界面不应该变成单独的 Mesh 首页。Mesh 是资源来源切换，不是新的主产品形态。

Desktop shell 保持当前 workspace/session/transcript 布局，只在左侧栏顶部提供当前 Host 选择器：

```text
[当前设备: 本机 MacBook Air]
  workspaces
  sessions

[当前设备: Mac mini · LAN]
  remote workspaces
  remote sessions
```

选择不同 Host 后，下方 workspace/session 列表显示所选 Host 的资源。主区域继续复用同一套 transcript、pending interaction、terminal、files 和 settings/trust surface。

UI 组件不应该到处分支判断本地/远端。上层应提供 Host-scoped `CoreClient`：

```text
selectedHost -> CoreClient
  本机 Host -> LocalCoreClient
  远端 Host -> RemoteCoreClient over E2EE
```

移动端也遵循同一产品模型，只是布局更窄：先选 Host，再使用该 Host 上的 workspace/session/PTY/files。

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
  创建 session、发送 prompt、中断 turn、取消/steer queued prompt、fork/delete session。发送 prompt 时必须由 Host/Core 明确判定 session input mode，而不是由 Controller UI 自行猜测。queued prompt 管理由 `core.control.queue.cancel` 和 `core.control.queue.steer` 暴露，Controller 只能传 session_id + queue_id；Host 负责确认 queued turn 是否仍存在并落 queue.cancelled / queue.steered 事件。session create/fork/delete 管理由 `core.control.session.create`、`core.control.session.fork` 和 `core.control.session.delete` 暴露，Host 负责复用本地 session 语义创建 session、创建 fork projection、停止 runtime、清理 queue 并落 session.started / session.deleted。

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
  面向大文件、生成中图片、未来视频或渐进式预览的媒体流。数据帧和 PTY 一样走 E2EE channel，relay 只转发密文。Host 返回 resume_token；Controller 可以在新的控制连接上用 resume_token + offset 重新发起 stream 来恢复读取，也可以用 media.stream.cancel 取消同一 control connection 上的 active stream。Host 必须按准备阶段的 expected size 读取；媒体文件在 stream 过程中变短或变长都不能返回 completed，必须返回 `media_stream_truncated`。v1 的恢复是 Host descriptor + offset replay，不是 relay 持久化、明文缓存或 transport migration。

workspace.files.read
  通过 Host 浏览目录或读取文件内容。SSH workspace 中，由 Host 发起 SSH 读取。v1 返回目录列表或中小文件的 base64 内容；大文件用 `workspace.files.stream` action，仍复用该 capability。

workspace.files.write
  通过 Host 创建、覆盖、精确文本编辑、删除或移动 workspace root 内的文件路径。SSH workspace 中，由 Host 发起 SSH 写入、删除或移动。复杂大文件流式读写仍作为独立能力后续扩展，不塞进普通 write response。

host.fs.browse
  用于创建 workspace 前浏览“当前所选 Host”的目录，只返回 root、当前目录和一层目录项元数据，不读取文件内容，不落事件日志，也不进入云端字段。本机 local、远端 local 都使用 Host 原生路径模型；Windows Host 必须返回 drive root 和反斜杠路径，Controller 只展示并原样回传路径，不能自行拼接或 normalize。SSH 模式由所选 Host 使用它自己的 SSH 配置和网络去浏览 SSH 目录，所以 remote ssh 是“远端 Desktop 去连 SSH”，不是 Controller 本机去连。

workspace.exec
  通过 Host 在 workspace root 内执行 command。SSH workspace 中，由 Host 发起 SSH exec。是否允许执行由 Host/Core 的 `workspace_exec_policy` 判断，Controller 不能自行绕过。

terminal.open
  打开或 attach Host 拥有的 PTY。

terminal.input
  向 Host 拥有的 PTY 发送原始按键输入。

host.manage
  管理 Host-owned 控制面能力。v1 开放 `host.trust.list`、`host.trust.revoke`、`host.pairing.list`、`host.pairing.approve`、`host.pairing.deny`，用于通过 E2EE control channel 查看 trusted devices、撤销某个 Controller、批准/拒绝 pending pairing request，并触发 Host 本地立即断开和 terminal viewer/input detach。创建/删除 workspace、连接/断开 SSH workspace、settings 和 updates 不塞进这个 v1 action。
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

chunked ingest 仍然走 encrypted control request/response，不开放明文 HTTP upload URL。Host 用 seq/offset 校验顺序写入自己的 upload store，finish 后才返回可用于 `core.control.session_input` 的 attachment handle。`attachment.ingest` 和 `attachment.ingest.start` 的 name、mime_type、detail 等 metadata 必须有明确长度上限，不能只限制文件内容或 chunk 大小。远程 `core.control.session_input` 只接受 Host-owned attachment handle，不接受 Controller 本地路径。

未完成的 chunked upload 是 Host-local 临时状态，不是可持久引用。Host 可以让长期未完成的 upload 过期；过期 upload 再次被 `chunk/finish` 触达时必须返回 `attachment_upload_expired` 并清理临时 metadata/part file。Controller 收到过期错误后只能重新 `attachment.ingest.start`，不能复用旧 `upload_id`。

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

远控 `core.read.events` 和 `core.subscribe.events` 返回的是 Host Gateway 投影后的事件，不是 JSONL 原始事件直出。投影必须去掉 `AstralEvent.raw`，移除 `native_session_id` / `native_thread_id` / `forked_from_native_anchor` 等 Host/runtime 内部标识，并从 `workspace.*` 事件中移除 `local_cwd`、`local_projection_root`、`ssh` 等 Host workspace 内部字段。`message.user.attachments`、`message.*.media`、`message.media` 等 transcript media surface 还必须移除 Host 私有 `path` / `saved_path` / `file_path` 字段。Controller 只能拿 `event_seq + media_id` 再通过 `media.read`、`media.download` 或 `media.stream` 读取内容。

同理，远控 `core.read.workspaces`、`core.read.workspace.connection`、`core.read.sessions`、`core.read.session_view` 返回 Host Gateway 投影后的 workspace/session 元数据和 Host-owned workspace 连接状态。Controller UI 只是遥控器：选中远端 Host 时，工作区列表、SSH 连接状态、session 状态和后续交互都必须以被控端 Host 返回的数据为准，不能混用 Controller 本机的 workspace/SSH 状态。workspace/session 投影保留 `id`、`name`、`target`、`agent`、状态和时间等远控 UI 所需字段，但不暴露 `local_cwd`、`local_projection_root`、SSH 配置对象、native session/thread id 等 Host/runtime 内部细节。`core.read.workspace.connection` 可以返回 UI 所需的连接状态和显示路径。`session_view.pending_interaction.detail_rows` 可以展示审批决策目标，但每行必须带机器可读 `key`（如 `cwd` / `path` / `command`）；Host Gateway 按 `key` 投影 cwd/path 为 workspace-relative display path，不能依赖 UI label，也不能把 Desktop 本机绝对路径或 SSH remote cwd 发给 Controller。

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
local terminal cwd confinement
bounded terminal.input payload
bounded terminal.output frame size
shared input from trusted Controllers
multi viewer
opened/attached/detached/closed lifecycle events only
trust revocation detaches that Controller without closing Host-owned PTY
```

这些 action 仍然经过 Host trust store 和 capability 校验。`terminal.attach` 必须发生在已完成握手的 encrypted control WebSocket 上，因为 PTY 输出只能回到这条 E2EE channel。`terminal.open` 的本地 cwd 必须和 workspace files/exec 一样做 workspace root confinement，包括拒绝通过 symlink 逃逸到 root 外。`terminal.open` response 和 terminal lifecycle event 里的 `cwd` 只能是 workspace-relative display cwd，不能暴露 Desktop 本机绝对路径或 SSH remote cwd；真实执行 cwd 只留在 Host 内部。`terminal.input`、`terminal.resize`、`terminal.close` 使用 `terminal.input` capability，因为它们都会改变 Host 侧 PTY 状态。`terminal.input` 是按键/粘贴输入，不是无限上传通道，必须有单次 payload 上限；PTY 输出 frame 也必须由 Host 拆成有界 E2EE frame。PTY 输出不进入 JSONL，只有 opened、attached、detached、closed lifecycle event 会落盘。

断线行为：

```text
Host 可以在短时间 retention window 内保留 PTY session。
Controller 重连后可以重新 attach。
多个 viewer 可以 attach。
多个可信 Controller 可以同时 input，Host 按 frame 到达顺序写入 PTY。
撤销 trusted device 会关闭该设备的 control session 并 detach viewer；不影响其他可信 Controller 的 input。
`writer_device_id` / `released_terminal_writers` 仅保留为兼容字段，shared-input 模式下通常为空或 0。
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
account_id_hash
account relay_id / relay_url
device_id
device_name
device_kind
public_key
public_key_fingerprint
capabilities
online_status
device relay_url presence hint
last_seen
updated_at
```

云端 device registry 只保存 public metadata 和路由元数据。账号 relay 配置是账号级默认值；device `relay_url` 只是该设备当前 heartbeat 暴露的 relay presence hint，应该由账号 relay 下发得到，不能演变成每台设备自选 relay 的复杂路由系统。云端不能保存 device private key、Host local cwd、SSH config、workspace/session/event 数据、prompt、approval 内容、文件树、附件或媒体明文。

Relay envelope 是不透明转发信封：

```text
version: astralops-relay-envelope-v1
connection_id
from_device_id
to_device_id
payload_kind: control.hello | control.hello_ack | control.sealed_frame
payload_base64
created_at
```

`payload_base64` 对 relay 始终是不透明 bytes，cloud 不接收 relay payload。`control.hello` / `control.hello_ack` 只能承载现有握手帧；`control.sealed_frame` 必须是 Controller 和 Host 已完成设备级 E2EE 后产生的 sealed frame。`connection_id` 是 relay routing metadata：`control.hello` 尚未产生连接 ID，可为空；`control.hello_ack` 和 `control.sealed_frame` 必须携带连接 ID，用来区分同一对设备之间的并发控制会话。`control.hello_ack` 必须回显 `hello.client_nonce`，并且该 nonce 进入 Host 签名 payload；Controller 用它识别和 ack 已过期的旧 hello_ack envelope，避免超时重试后 relay 队列堆积。Relay 可以按 `from_device_id` / `to_device_id` / `connection_id` 路由、按账号限流、投递确认或断开，但不能解析 payload，也不能把 workspace/session/event payload 提升成云端字段。

relay envelope 被目标设备成功处理后必须调用：

```text
POST /v1/relay/envelopes/:envelope_id/ack
body: { "device_id": "<to_device_id>" }
```

relay 只在 `device_id == to_device_id` 时删除该 envelope。客户端不能把 `GET /v1/relay/envelopes` 当作历史查询接口；它是待投递队列视图，未 ack 的 envelope 会继续出现。长轮询超时返回空列表不是错误，客户端应立即继续下一轮等待，直到本地 control session 超时、取消或收到目标 frame。

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

正式 pairing 不是直接写 trust grant。未可信设备只能提交 Host-owned pairing request：

```text
POST /v1/pairing/requests
GET /v1/pairing/requests/:request_id
```

请求必须包含：

```text
controller_device_id
controller_device_name
controller_device_kind
controller_public_key
controller_public_key_fingerprint
requested capabilities / scope
```

`POST /v1/pairing/requests` 可以暴露在 Host remote listener 上，因为它只创建 pending request，不授予控制权。它必须校验 controller public key 和 fingerprint，不能接受缺失 public key 的正式配对请求。Host 不能因为同账号、LAN 可见或 relay 可达就自动批准。

批准/拒绝只能由本机 authenticated Host UI 或已经可信且具备 `host.manage` capability 的 Controller 执行：

```text
GET /v1/pairing/requests
POST /v1/pairing/requests/:request_id/approve
POST /v1/pairing/requests/:request_id/deny

host.pairing.list
host.pairing.approve
host.pairing.deny
```

批准会把 pending request 转成本地 trust grant，并写入 `control.trust.granted` / `control.pairing.approved` audit event；拒绝只写入 `control.pairing.denied`，不会写 trust grant。已经 resolved 的 pairing request 不能再次批准。

当前 daemon 已落地：

```text
host.trust.list over E2EE control channel
host.trust.revoke over E2EE control channel
host.pairing.list/approve/deny over E2EE control channel
local pairing request submit/list/approve/deny HTTP endpoints
local trust revoke HTTP endpoint
immediate active control session close
terminal viewer/input detach on revoke
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
本机 Host 或已有可信设备批准新设备
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

`core.subscribe.events` v1 是最小事件订阅协议：Controller 先用 `core.read.events` 按 `after_seq` 拉取窗口，再在同一条 E2EE control connection 上订阅 Host event frame。为了避免读窗口和订阅之间的竞态，订阅请求可以带 `after_seq + replay_limit`，Host 会先发送符合过滤条件的 replay frame，再发送后续 live frame。`core.unsubscribe.events(stream_id)` 取消订阅；连接断开、设备踢出信任或 control session 关闭时，Host 必须清理该连接上的 event subscription。

当前 daemon 的最小控制通道入口：

```text
GET /v1/control/ws
```

当前 relay MVP 已落地 Host 侧 envelope long-polling、Controller 侧 encrypted frame transport 和独立 `relay/` 服务：当 LAN discovery/direct WebSocket 不可用且目标 Host 是已知可信设备、cloud registry 显示在线时，本机 daemon 可以通过 relay service 投递 `control.hello` / `control.hello_ack` / `control.sealed_frame` 完成同一套 E2EE 握手，并在同一条逻辑 control connection 上承载 request/response、event subscription、workspace/media stream、attachment chunk ingest 和 remote PTY frame。

relay streaming 不是云端业务代理：`control.sealed_frame` 内部明文只存在于 Controller 和 Host 设备内存中。Broker 只能长轮询、存储待投递密文 envelope、ack 删除和按账号/设备做基础限流；不能缓存明文事件、文件、媒体、PTY 输出或 SSH 配置。连接关闭、设备踢出信任或 idle 过期时，Host 必须清理 relay control session 绑定的 stream context 和 PTY viewer。

Desktop Controller 不直接在 React/Electron renderer 内实现远控握手，也不持有远控私钥。当前桌面端先通过本机 daemon 暴露 controller-side 代理 API，再由本机 daemon 复用同一套 Host identity、known_hosts、LAN discovery 和 E2EE control channel：

```text
GET /v1/remote/hosts?discover=1
GET /v1/remote/hosts/:host_device_id/snapshot
GET /v1/remote/hosts/:host_device_id/host
POST /v1/remote/hosts/:host_device_id/fs/browse
GET /v1/remote/hosts/:host_device_id/workspaces
POST /v1/remote/hosts/:host_device_id/workspaces
POST /v1/remote/hosts/:host_device_id/workspaces/:workspace_id/connect
POST /v1/remote/hosts/:host_device_id/workspaces/:workspace_id/disconnect
GET /v1/remote/hosts/:host_device_id/sessions
GET /v1/remote/hosts/:host_device_id/sessions/:session_id/view
GET /v1/remote/hosts/:host_device_id/events
GET /v1/remote/hosts/:host_device_id/events?stream=1
GET /v1/remote/hosts/:host_device_id/workspaces/:workspace_id/files
GET /v1/remote/hosts/:host_device_id/workspaces/:workspace_id/pty
GET /v1/remote/hosts/:host_device_id/pairing/requests
POST /v1/remote/hosts/:host_device_id/pairing/requests/:request_id/approve
POST /v1/remote/hosts/:host_device_id/pairing/requests/:request_id/deny
POST /v1/remote/hosts/:host_device_id/workspaces/:workspace_id/exec
POST /v1/remote/hosts/:host_device_id/sessions
POST /v1/remote/hosts/:host_device_id/sessions/:session_id/input
POST /v1/remote/hosts/:host_device_id/sessions/:session_id/interrupt
POST /v1/remote/hosts/:host_device_id/sessions/:session_id/fork
POST /v1/remote/hosts/:host_device_id/sessions/:session_id/edit-last-user-message
DELETE /v1/remote/hosts/:host_device_id/sessions/:session_id
POST /v1/remote/hosts/:host_device_id/sessions/:session_id/queue/:queue_id/cancel
POST /v1/remote/hosts/:host_device_id/sessions/:session_id/queue/:queue_id/steer
POST /v1/remote/hosts/:host_device_id/approvals/:interaction_id/respond
```

这些 endpoint 是本机 daemon 的 controller-side facade，不是 Host remote listener。它们必须只对本机 authenticated Desktop 开放，并且要求本机已加入 Cloud Mesh；真正的远端执行仍然通过目标 Host 的 `/v1/control/ws`，目标 Host listener/relay hello 也必须要求 Host 当前 Cloud Mesh active。`discover=1` 只把 Cloud Mesh 列表中已存在、LAN 上匹配 known host fingerprint 且通过 `/v1/host` 校验的 Host 标为 `lan`，未知设备和本地 stale known host 不能自动出现在可控列表里。

当前 Desktop 的 Host selector 使用这个 facade 拉取远端 Host 和 Host-scoped workspaces/sessions/events。切换 Host 时优先调用 `core.read.host_snapshot` / `/snapshot` 一次性读取 Host info、workspaces、sessions、workspace connection、recent events 和 session views，避免 renderer 对远端 Host 发起多次串行状态请求；snapshot 仍然必须走 Core sanitization，不能把 raw payload、native runtime id、Host helper path、Host 本地路径或 SSH 私有配置泄露给 Controller。创建 workspace 使用同一套 Host-scoped 目录选择器：本机 local、远端 local、本机 ssh、远端 ssh 都先通过当前 CoreClient 调用 `host.fs.browse`，再用 `core.control.workspace.create` 在所选 Host 上创建；SSH workspace 的 connect/disconnect 也通过 Host 侧 `core.control.workspace.connect/disconnect` 执行。远程 PTY 通过本机 daemon WebSocket facade 接入：Desktop 仍打开 `/v1/remote/hosts/:host_device_id/workspaces/:workspace_id/pty`，本机 daemon 再通过目标 Host 的 encrypted control WebSocket 转发 `terminal.open/attach/input/resize/close`，并把 `terminal.output` / `terminal.closed` frame 映射回本地终端 UI 的 ready/output/exit 消息。远端 Host 的 pending pairing request 管理由本机 daemon facade 转成 `host.pairing.*` E2EE action；发起 cloud pairing request 前，本机 daemon 会先注册当前 Controller public identity，避免首次启动还未 heartbeat 时请求失败；approved cloud pairing signal 只会导入 Host public identity 到 Controller `known_hosts`，不会写任何 Host trust grant。Desktop renderer 不直接持有远控密钥。尚未进入 control protocol 的本地 app 设置和 session command 列表不能在 UI 里伪造；下一步应为这些能力补明确协议 action 或保持不可用状态。

握手和帧语义：

```text
hello
  controller_device_id
  controller_public_key
  controller_ephemeral_key
  client_nonce
  membership_lease
  Ed25519 signature

hello_ack
  host_device_id
  host_public_key
  host_ephemeral_key
  client_nonce
  server_nonce
  connection_id
  membership_lease
  Ed25519 signature

sealed
  seq
  nonce
  AES-GCM ciphertext
```

会话密钥由 X25519 临时密钥交换派生。`request/response/close` 等业务帧只能放进 `sealed`，不能以明文 JSON 发送。Host 收到 request 后必须覆盖或校验 `controller_device_id`，然后进入 Host Gateway 执行 capability/trust 检查。

Host control WebSocket 必须设置 read limit：hello 只允许小型握手帧，sealed frame 只允许能承载当前同步能力上限的有限 payload。超过上限的请求应关闭 control connection；大文件、大媒体和持续输出必须使用对应 chunk/stream/PTY 能力，不能把单个 E2EE WebSocket message 做成无限大上传。

Host 必须独立监听 control transport 的 close/read error，而不能等当前同步 action 返回后才发现断线。断线、close frame、非法 encrypted frame 或 trust revoke 都要立即取消该 connection context，并向所有绑定在这条连接上的 Host-owned action 传播取消。

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
media.read / media.download bounded to medium payloads; large media must use media.stream
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
workspace.exec bounded stdout/stderr/output metadata
local workspace root confinement
SSH workspace Host-initiated read/range-read/write/delete/move/exec
E2EE request/response frames
chunked E2EE workspace_file frames
no Host local absolute root in workspace.files.read response

待落地:
async workspace.exec approval interaction, if product needs per-command confirmation UI
```

`workspace.files.read` v1 用于目录列表和中小文件读取；文件内容以 base64 放在 encrypted control response 中。`workspace.files.write` action v1 只创建或覆盖单个文件，并且同步 request payload 必须有明确大小上限；大文件写入应后续扩展独立 chunked write 能力，而不是把单个 encrypted request 做成无限大上传。精确编辑、删除、移动和大文件读取分别使用独立 action，避免把文件管理语义塞进一个过宽 action。

`workspace.files.apply_patch` v1 是 Host 侧精确文本编辑能力：Controller 提交 `old_string`/`new_string` edits，Host 在 workspace root 内读取目标文件，要求默认单次匹配唯一；只有显式 `replace_all` 才允许替换多处。patch 输入和结果也必须受同步文件写入大小边界约束，不能用小 old_string 替换成超大 new_string 来绕过 `workspace.files.write` 上限。它不解析完整 unified diff，不 shell out 到 `patch`，也不做跨端文件同步。这样先满足远控编辑需要，同时把 delete/move/大文件流式读写作为独立 action 后续扩展，避免把文件管理语义一次性塞进一个过宽 action。

`workspace.files.delete` 和 `workspace.files.move` v1 是独立 Host 侧文件管理 action，复用 `workspace.files.write` capability。它们只接受 workspace root 内的路径，不能删除或移动 workspace root 本身；删除目录必须显式 `recursive=true`；移动默认不覆盖已有目标，只有显式 `overwrite=true` 才允许覆盖非目录目标。SSH workspace 中由 Host 通过 proxy helper 发起 `remove`/`move`，Controller 不接触远端路径之外的本机文件系统能力。

`workspace.files.stream` v1 用于大文件读取：普通 `workspace.files.read` 仍负责目录列表和中小文件 base64 response；超过普通 response 适合承载的内容由 Controller 请求 `workspace.files.stream`，Host 返回 stream metadata 后在同一 encrypted control connection 上发送 `workspace_file.chunk` / `workspace_file.completed` / `workspace_file.error` frame。Controller 可以通过 offset 重新请求来恢复读取，也可以用 `workspace.files.stream.cancel` 取消当前 control connection 上的 active stream。Host 必须按准备阶段的 expected size 读取；文件在 stream 过程中变短或变长都不能返回 completed，必须返回 `workspace_file_stream_truncated`。SSH workspace 通过 proxy helper 的 `read_range` 从远端按 chunk 读取，Host 只转发密文 frame，不生成可被 relay 或云端读取的明文 URL。

`workspace.exec` v1 由 Host trust grant 的 `workspace_exec_policy` 决定是否执行。默认 `trusted` 表示 `workspace.exec` capability 本身就是执行授权；`require_approval` 会同步拒绝执行并返回 `workspace_exec_approval_required`，不会启动本地或 SSH command；`disabled` 直接拒绝为 `workspace_exec_disabled`。这先把 command approval policy 放进 Core/daemon 决策层，避免 Controller 自行判断或把 gateway command 伪装成 Claude/Codex 原生 approval。若未来需要逐条命令确认，应该在这个 policy gate 之上新增 Host-owned async interaction，而不是让客户端绕过策略。

`workspace.exec` v1 是同步 request/response 能力，不是长输出流。Host 必须限制 stdout/stderr/output 的响应大小，并在结果里返回 `stdout_truncated` / `stderr_truncated` / `output_truncated` / `output_bytes_limit` 这类 metadata。需要持续输出、交互输入或大输出的场景应该使用 Host-owned PTY/terminal 能力，而不是把同步 exec response 扩成无限大 payload。`workspace.exec` 必须绑定发起它的 control connection context；Controller 断线、Host 主动关闭连接或 trust revoke 时，Host 要取消对应本地/SSH exec，而不是让命令脱离远控连接继续运行。

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
local terminal cwd confinement
shared input from trusted Controllers
multi viewer
retention timeout
trust revocation detaches that Controller without closing Host-owned PTY
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
