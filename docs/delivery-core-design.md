# Delivery Core 设计

> 状态：Implemented v1
>
> 本文记录 Delivery Core 的设计边界。当前 Flame Chat 的逐字段线上行为以 [protocol.md](protocol.md) 为准；本文中的 Stream snapshot 和外部 Bus adapter 仍属于后续演进项。

## 1. 摘要

Delivery Core 是一个可嵌入 Go 服务的消息引擎。宿主应用提供身份认证和存储，引擎提供 WebSocket 接入、Room、成员关系、权限校验、消息投递、幂等、排序和断线恢复。

核心只认识五个概念：

```text
Identity -> Room -> Member -> Message -> Delivery
```

核心不认识聊天产品中的联系人、会话标题、头像、未读数、邀请、OIDC、WebRTC 或 AI。应用可以用统一 Message 承载这些业务事件，但其含义由业务层定义。

期望的最小使用方式：

```go
engine, err := delivery.New(delivery.Options{
	Authenticate: authenticateRequest,
	Store:        store,
})
if err != nil {
	return err
}
defer engine.Close()

mux.Handle("/realtime", engine.Handler())
```

宿主应用随后可以创建 Room、增加成员和投递消息：

```go
room, err := engine.CreateRoom(ctx, delivery.CreateRoom{
	ID:        "room-123",
	CreatorID: "alice",
})

err = engine.AddMember(ctx, delivery.AddMember{
	ActorID:    "alice",
	RoomID:     room.ID,
	IdentityID: "bob",
	Grants:     delivery.MemberGrants(),
})

receipt, err := engine.Publish(ctx, delivery.Publish{
	ID:      "019ff9c8-7e80-7000-a801-0123456789ab",
	RoomID: room.ID,
	ActorID: "alice",
	Name:    "message.created",
	Profile: delivery.Durable,
	Data:    json.RawMessage(`{"type":"text","text":"hello"}`),
})
```

客户端连接后不需要手动维护 Room 订阅。引擎根据持久成员关系自动决定当前 Identity 可以接收哪些消息。

## 2. 目标

Delivery Core 的目标是：

1. 用一个稳定协议承载持久消息、临时信号和流式片段。
2. 让客户端 SDK 统一处理连接、重试、去重、恢复和本地 outbox。
3. 让用户标识保持不透明，可以是 UUID、Email、用户名或其他稳定字符串。
4. 按 Room 提供成员隔离和最小权限控制。
5. 单实例只依赖内存和 Store，多实例可以替换 Bus 而不改变客户端协议。
6. 保持嵌入式设计，宿主应用不需要额外部署一个消息服务才能开始使用。

核心成功的判断标准不是“支持多少功能”，而是宿主应用只需完成以下闭环：

```text
Authenticate
  -> Connect
  -> CreateRoom
  -> AddMember
  -> Publish
  -> Deliver
  -> Reconnect
  -> Recover
```

## 3. 非目标

第一阶段明确不提供：

- 用户注册、OIDC、Session 签发和资料管理。
- 联系人、邀请、会话标题、头像、已读和未读等聊天产品模型。
- 文件存储和大文件传输；消息只携带文件引用和元数据。
- 任意 Topic、通配符订阅、共享订阅和 Room 层级。
- 跨 Room 的全局顺序。
- 跨地域 Exactly Once 承诺。
- APNs、PushKit、Email 或 Webhook 投递实现。
- 消息搜索、内容审核、计费和分析系统。
- MQTT、NATS、Redis 等特定基础设施的强制依赖。
- 由客户端直接创建角色或修改自己的权限。

## 4. 分层边界

```text
Application
  - Authentication and sessions
  - User profiles and contacts
  - Conversation metadata
  - Product roles
  - Read state and notifications
  - Calls, bots and message schemas
            |
            v
Delivery Core
  - Connections and identities
  - Rooms and memberships
  - Capabilities
  - Envelope validation
  - Ordering and idempotency
  - Realtime routing and recovery
            |
      +-----+-----+
      v           v
    Store         Bus
  source of     live fanout
    truth       only
```

### 4.1 宿主应用负责

- 将 HTTP/WebSocket 请求认证为稳定 Identity ID。
- 决定什么业务动作会创建 Room 或修改成员。
- 把 owner、admin、member 等产品角色映射为核心能力。
- 定义消息 `name` 和 `data` 的业务 schema。
- 决定哪些业务事件触发推送、未读数或审计。
- 管理 Blob、图片、音频和其他大型对象。

### 4.2 Delivery Core 负责

- WebSocket 握手、保活、关闭和恢复。
- 将经过认证的 Identity 与一个或多个设备连接绑定。
- 根据 Room membership 自动建立和撤销路由。
- 校验发布权限、消息大小、速率和 envelope。
- 为持久消息提供幂等写入和 Room 内连续序号。
- 将消息投递到该 Room 成员的所有在线连接，包括发送者自己的其他连接。
- 为断线客户端补齐持久消息。
- 对慢客户端实施背压，不让低价值流量挤掉控制消息。

## 5. 核心模型

### 5.1 Identity

```go
type Identity struct {
	ID string
}
```

`ID` 是不透明、稳定、非空的 UTF-8 字符串。核心不解析其格式，也不保存 Email、名称、头像等资料。

建议默认限制：

- 长度不超过 255 bytes。
- 精确区分大小写；需要 Email 大小写归一化时由宿主应用完成。
- 一经用于持久记录，不应静默改变。

一个 Identity 可以同时拥有多个连接。连接是临时对象，不等同于用户或设备。

### 5.2 Room

```go
type Room struct {
	ID        string
	CreatedAt time.Time
}
```

Room 是隔离、授权、排序和恢复的最小范围。核心不区分私聊、群聊、通话或 AI Session。

Room ID 由调用方提供或由引擎生成，但在系统内必须唯一。删除 Room 后默认不允许复用原 ID。

### 5.3 Member

```go
type Member struct {
	RoomID       string
	IdentityID   string
	Grants       Grants
	HistoryStart int64
	CreatedAt    time.Time
}
```

Member 是持久授权关系。它与客户端是否在线、是否正在查看页面以及 WebSocket 是否连接无关。

成员加入后，其现有连接立即获得该 Room 的消息路由；成员移除后，其现有连接立即失去路由。重新连接时始终以 Store 中的成员关系重新计算，不能信任客户端保存的订阅状态。

### 5.4 Message

```go
type Message struct {
	ID        string
	PublishID string
	RoomID    string
	ActorID   string
	Name      string
	Profile   Profile
	Sequence  int64
	Data      json.RawMessage
	CreatedAt time.Time
	ExpiresAt *time.Time
}
```

核心只解释 envelope，不解释 `Data`。核心生成或覆盖 `ActorID`、`Sequence` 和 `CreatedAt`，不能信任客户端提供这些字段。

建议 `Name` 使用小写、点分隔命名：

```text
message.send
message.created
rtc.signal
typing.changed
ai.output.delta
ai.output.completed
```

命令和事实应使用不同名称。客户端可以请求 `message.send`，但最终投递的持久事实由业务处理器定义为 `message.created`。

## 6. 权限模型

核心使用能力而不是固定角色：

```go
type Capability string

const (
	Receive       Capability = "room.receive"
	Publish       Capability = "message.publish"
	ReadHistory   Capability = "history.read"
	ManageMembers Capability = "members.manage"
	ManageRoom    Capability = "room.manage"
)

type Grants map[Capability]bool
```

建议提供便捷预设，但预设不是协议概念：

| 应用角色示例 | receive | publish | history | members.manage | room.manage |
|---|---:|---:|---:|---:|---:|
| owner | yes | yes | yes | yes | yes |
| admin | yes | yes | yes | yes | no |
| member | yes | yes | yes | no | no |
| readonly | yes | no | yes | no | no |
| bot | yes | yes | yes | no | no |

规则：

1. WebSocket 发布必须拥有 `message.publish`。
2. 实时接收必须拥有 `room.receive`。
3. 恢复历史必须同时拥有 `room.receive` 和 `history.read`。
4. 成员和 Room 管理默认只通过宿主应用的 Go API/HTTP API，不作为第一版 WebSocket 指令开放。
5. 非系统调用者不能授予自己不具备的能力。
6. owner 必须存在、最后一个 owner 是否可以退出等产品约束由宿主应用决定。

核心能力负责安全下限；宿主应用仍可在调用核心前执行更严格的业务策略。

## 7. 投递 Profile

```go
type Profile string

const (
	Durable   Profile = "durable"
	Ephemeral Profile = "ephemeral"
	Stream    Profile = "stream"
)
```

三种 Profile 共用一个 Envelope 和连接，但拥有不同保证。协议统一不意味着所有消息具有相同可靠性。

### 7.1 Durable

适用于不能丢失的最终事实，例如普通消息、最终 AI 回复和通话结果。

保证：

- 成功 ACK 前已经写入 Store。
- 每个 Room 拥有从 1 开始严格递增、无重复的 `sequence`。
- 同一发布者在同一 Room 重试同一个客户端消息 ID，只产生一个持久事件。
- 客户端可以使用 sequence 发现缺口并恢复。
- 允许网络层重复投递，客户端 SDK 必须按 `(room_id, sequence)` 去重。

该模型是“至少一次传输 + 幂等持久化 + 客户端去重”，不声明端到端 Exactly Once。

### 7.2 Ephemeral

适用于失效后没有恢复价值的实时信号，例如 typing、presence 和 WebRTC signaling。

保证：

- 服务器完成认证、授权和基本校验后返回 `accepted`。
- 只尝试投递给当前在线接收者。
- 不分配 durable sequence，不进入永久历史。
- 可以设置 TTL，过期消息必须丢弃。
- `accepted` 不表示任何对方设备已经收到或处理。

### 7.3 Stream

适用于 AI token、语音转写和进度等不断变化、最终收敛的输出。

```go
type StreamPosition struct {
	ID    string
	Seq   uint64
	Final bool
}
```

建议语义：

- 按 `(room_id, stream_id, stream_seq)` 去重和排序。
- 中间 delta 可以合并，慢客户端只需获得较新的完整快照。
- Store 可以保存带 TTL 的当前快照，但不永久保存所有 token。
- `final=true` 结束该 stream；业务通常再产生一条 Durable 最终消息。
- 第一版实现可以只支持 Durable 和 Ephemeral，但 wire envelope 预留 Stream 字段。

## 8. Go API

### 8.1 创建引擎

```go
type Options struct {
	Authenticate        Authenticator
	Store               Store
	Bus                 Bus
	Limits              Limits
	OriginCheck         func(*http.Request) bool
	HandleClientPublish ClientPublishHandler
}

type Authenticator func(
	context.Context,
	*http.Request,
) (Identity, error)

func New(Options) (*Engine, error)
func (e *Engine) Handler() http.Handler
func (e *Engine) Close() error
```

缺少 `Authenticate` 或 `Store` 时 `New` 应失败。`Bus` 可选，默认使用单进程 Memory Bus。

### 8.2 Room 和成员

```go
type CreateRoom struct {
	ID        string
	CreatorID string
}

type AddMember struct {
	ActorID    string
	RoomID     string
	IdentityID string
	Grants     Grants
}

type UpdateMember struct {
	ActorID    string
	RoomID     string
	IdentityID string
	Grants     Grants
}

type RemoveMember struct {
	ActorID    string
	RoomID     string
	IdentityID string
}

func (e *Engine) CreateRoom(context.Context, CreateRoom) (Room, error)
func (e *Engine) AddMember(context.Context, AddMember) error
func (e *Engine) UpdateMember(context.Context, UpdateMember) error
func (e *Engine) RemoveMember(context.Context, RemoveMember) error
func (e *Engine) DeleteRoom(context.Context, actorID, roomID string) error
```

当前调用面保持最小；需要更多查询时优先由宿主业务提供，不把业务读模型塞入核心。

系统初始化、数据迁移和可信后台任务可能需要绕过成员权限。此能力必须通过显式命名的 trusted API 提供，不能通过空 `ActorID` 或特殊字符串暗示。

### 8.3 服务端发布

```go
type PublishRequest struct {
	ID        string
	RoomID    string
	ActorID   string
	Name      string
	Profile   Profile
	Data      json.RawMessage
	ExpiresAt *time.Time
	Stream    *StreamPosition
}

type Receipt struct {
	PublishID string
	MessageID string
	Status    ReceiptStatus
	Sequence  int64
}

func (e *Engine) Publish(context.Context, PublishRequest) (Receipt, error)
```

服务端发布同样经过成员和权限校验。AI Bot 应作为具有 `message.publish` 的普通 Identity 加入 Room，而不是伪造其他用户。

## 9. Wire Protocol

建议通过 WebSocket Subprotocol 协商版本：

```http
Sec-WebSocket-Protocol: delivery.v1
```

所有 JSON 字段使用 `snake_case`。第一版使用 UTF-8 JSON，文本 frame 是规范形式；服务端兼容内容为 UTF-8 JSON 的 binary frame，便于原生客户端迁移。MessagePack/Protobuf 等二进制编码不在首版范围。

### 9.1 Hello

认证和升级成功后，服务器首先发送：

```json
{
  "op": "hello",
  "protocol": "delivery.v1",
  "connection_id": "019ff9d1-...",
  "identity_id": "alice",
  "max_message_bytes": 65536
}
```

### 9.2 Resume

客户端提供已经连续处理完成的 Room cursor：

```json
{
  "op": "resume",
  "rooms": {
    "room-123": 41,
    "room-456": 18
  }
}
```

服务器只接受该 Identity 当前仍有权读取的 Room。客户端提供未知或无权访问的 Room 时，不得泄露 Room 是否存在。

Room 数量很大时，resume 必须支持分页或增量批次；第一版可设置数量上限。

### 9.3 Publish

```json
{
  "op": "publish",
  "id": "019ff9c8-7e80-7000-a801-0123456789ab",
  "room_id": "room-123",
  "name": "message.send",
  "profile": "durable",
  "data": {
    "type": "text",
    "text": "hello"
  }
}
```

客户端不能提供可信 `actor_id`、`sequence`、`created_at` 或服务端 event ID。

### 9.4 ACK

Durable 成功：

```json
{
  "op": "ack",
  "id": "019ff9c8-7e80-7000-a801-0123456789ab",
  "status": "committed",
  "event_id": "019ff9d4-...",
  "sequence": 42
}
```

Ephemeral 成功：

```json
{
  "op": "ack",
  "id": "019ff9c8-7e80-7000-a801-0123456789ab",
  "status": "accepted"
}
```

ACK 必须明确区分：

- `accepted`：服务器接受并尝试路由。
- `committed`：持久事件已经写入 Store。
- 不提供“对方已读”或“所有设备已收到”的隐含含义。

### 9.5 Event

```json
{
  "op": "event",
  "room_id": "room-123",
  "id": "019ff9d4-...",
  "name": "message.created",
  "profile": "durable",
  "sequence": 42,
  "actor_id": "alice",
  "data": {
    "type": "text",
    "text": "hello"
  },
  "created_at": "2026-08-13T16:00:00Z"
}
```

事件发送给所有拥有 `room.receive` 的在线成员连接，默认包括发布者。客户端通过自己发布的 ID 或业务 correlation 字段合并 optimistic state。

### 9.6 Sync 边界

恢复开始：

```json
{
  "op": "sync.begin",
  "room_id": "room-123",
  "after_sequence": 41,
  "head_sequence": 46
}
```

随后按升序发送 42 至 46，最后发送：

```json
{
  "op": "sync.end",
  "room_id": "room-123",
  "sequence": 46
}
```

同步期间到达的新事件可以被客户端暂存。客户端按 sequence 排序和去重，因此历史与实时交界处即使重复也不能产生重复业务效果。

### 9.7 Room 路由变化

成员加入或移除时，在线连接可以收到控制消息：

```json
{"op":"room.added","room_id":"room-123"}
```

```json
{"op":"room.removed","room_id":"room-123","reason":"membership_removed"}
```

这些控制消息只是实时提示。重新连接后，Store 中的成员列表是唯一权威来源。

### 9.8 Error

```json
{
  "op": "error",
  "request_id": "019ff9c8-...",
  "error": {
    "code": "permission_denied",
    "message": "publish permission is required",
    "retryable": false
  }
}
```

稳定错误码至少包括：

- `invalid_message`
- `message_too_large`
- `permission_denied`
- `room_not_found`
- `rate_limited`
- `conflict`
- `temporarily_unavailable`
- `sync_required`

面向未授权客户端时，`room_not_found` 和无权访问应当统一响应，避免枚举 Room。

## 10. 一致性与恢复

### 10.1 Durable 发布

持久发布必须在一个 Store transaction 内完成：

1. 验证当前 membership 和 publish 权限。
2. 按 `(room_id, actor_id, publish_id)` 查询幂等记录。
3. 已存在时返回原 Receipt。
4. 原子增加 Room sequence。
5. 写入 Event。
6. 提交 transaction。
7. 返回 `committed` ACK 并发布到 Bus。

如果第 6 步成功、第 7 步前进程崩溃，客户端会因为没有收到 ACK 使用相同 ID 重试；Store 必须返回原事件。其他客户端通过 cursor 恢复遗漏的实时通知。

### 10.2 顺序

- Durable 只保证单 Room 内的 Store 顺序。
- 同一事件的所有消费者使用 `sequence` 得到相同确定顺序。
- Ephemeral 不占用 Durable sequence。
- Stream 使用独立的 `stream_seq`，不能推进 Durable cursor。
- 不使用客户端时间戳判断最终顺序。

### 10.3 客户端 outbox

客户端 SDK 对 Durable 发布应当：

1. 先将 publish envelope 和稳定 ID 写入本地 outbox。
2. 显示 optimistic state。
3. 连接可用时发布。
4. 超时后使用相同 ID 指数退避重试。
5. 收到 committed ACK 或对应 Event 后完成合并并删除 outbox 项。

UI 只能观察 `queued`、`publishing`、`committed` 和明确失败状态，不能自行启动另一套 retry timer。

## 11. 自动 Room 路由

第一版不向客户端暴露通用 `subscribe` 和 `unsubscribe`。

连接成功时：

1. Authenticate 得到 Identity。
2. Store 返回该 Identity 当前拥有 `room.receive` 的 Room。
3. Hub 将连接挂到这些 Room。
4. 客户端提交已有 cursor，核心自动恢复允许读取的历史。

成员改变时：

- AddMember 提交成功后更新当前实例路由，并通过 Bus 通知其他实例。
- RemoveMember 提交成功后立即撤销路由。
- 已被移除的连接不能继续恢复历史或发布。
- 所有连接重建时重新读取 membership，避免缓存权限永久残留。

如果未来存在数万 Room 的 Identity，可以增加按需 attach，但它是性能优化，不改变 membership 才是授权依据这一原则。

## 12. Store 接口

建议把 Store 组合成小接口，测试和部署可以分别替换：

```go
type Store interface {
	RoomStore
	MemberStore
	EventStore
}

type RoomStore interface {
	CreateRoom(context.Context, Room, Member) error
	DeleteRoom(context.Context, string) error
	Room(context.Context, string) (Room, error)
}

type MemberStore interface {
	AddMember(context.Context, Member) error
	UpdateMember(context.Context, Member) error
	RemoveMember(context.Context, roomID, identityID string) error
	Member(context.Context, roomID, identityID string) (Member, error)
	RoomsForIdentity(context.Context, identityID string) ([]Room, error)
}

type EventStore interface {
	Append(context.Context, PublishRequest) (Message, error)
	EventsAfter(context.Context, roomID string, sequence int64, limit int) ([]Message, error)
	HeadSequence(context.Context, roomID string) (int64, error)
}
```

`Append` 必须承担原子 sequence 和幂等约束，不能由 Engine 先读 sequence 再写入。

计划提供的适配器：

- Memory Store：单元测试、示例和临时进程。
- PostgreSQL Store：生产持久化。
- Stream Snapshot Store：未来可选，允许使用内存或 Redis，不属于首版 Store 必需接口。

建议关系模型：

```text
rooms
  id, last_sequence, created_at, deleted_at

room_members
  room_id, identity_id, grants, created_at

room_events
  room_id, sequence, event_id, actor_id,
  publish_id, name, data, created_at
```

关键约束：

```text
PRIMARY KEY (room_id, sequence)
UNIQUE (room_id, actor_id, publish_id)
UNIQUE (room_id, identity_id)
```

成员关系是控制面状态，不强制占用 message sequence。宿主应用如需展示“某人加入 Room”，可以额外发布一个 Durable 业务事件。

## 13. Bus 与多实例

Bus 只负责在线 fanout，不是持久事实来源：

```go
type Bus interface {
	Publish(context.Context, BusMessage) error
	Subscribe(func(BusMessage)) (unsubscribe func(), err error)
	Close() error
}
```

单实例默认使用 Memory Bus。未来可以实现：

- NATS Bus
- MQTT Bus
- Redis Pub/Sub Bus
- PostgreSQL LISTEN/NOTIFY Bus

Bus 允许重复和短暂丢失，因为 Durable 客户端最终通过 Store cursor 恢复。Ephemeral 在 Bus 故障时可能丢失，这是其 Profile 定义的一部分。

MQTT 如果被采用，应隐藏在 Bus 适配器后面。客户端不直接管理 MQTT Topic、Session、QoS 或 ACL，Delivery wire protocol 和 SDK 不受基础设施替换影响。

## 14. 背压与慢客户端

一个 WebSocket 可以承载所有 Profile，但内部至少需要三个逻辑 lane：

1. `control`：hello、ack、error、sync 边界和权限变化。
2. `durable`：带 sequence 的持久事件。
3. `realtime`：ephemeral 和 stream delta。

优先级为 control、durable、realtime。

规则：

- control 不能被 AI token 或 typing 挤出队列。
- durable 队列溢出时可以关闭连接，客户端重连后按 cursor 恢复。
- ephemeral 队列溢出时允许丢弃过期消息。
- stream 队列溢出时优先用最新 snapshot 覆盖旧 delta。
- 不对无限慢的连接提供无限内存队列。

建议首版默认限制：

```text
maximum WebSocket frame       64 KiB
maximum message data          60 KiB
maximum resume rooms          configurable
maximum connections/identity  configurable
publish rate                  configurable per identity and room
ephemeral TTL                 bounded by server configuration
```

具体默认数值在实现和压测阶段确定，不作为首版协议永久承诺。

## 15. 安全要求

- WebSocket Upgrade 前完成认证和 Origin 校验。
- Identity 永远来自 Authenticator，不能来自客户端 JSON。
- 每次 publish 都验证当前 membership，不能只依赖连接建立时的缓存。
- 成员变更立即撤销现有连接权限。
- `data` 有总大小限制，宿主应用可以增加 name-specific validator。
- 日志不记录认证凭据、完整消息正文或第三方密钥。
- 错误不能泄露无权访问的 Room 是否存在。
- 服务端生成 connection ID、event ID、sequence 和 server timestamp。
- Trusted/system API 必须显式区分，禁止使用魔法 Identity ID 绕过权限。
- Rate limit 是核心安全边界，不应完全交给反向代理。

## 16. 业务集成示例

### 16.1 Flame Chat

| Chat 概念 | Delivery 映射 |
|---|---|
| conversation | Room |
| user ID | Identity ID |
| conversation member | Member |
| owner/admin/member | Grants presets |
| message | Durable Message |
| typing | Ephemeral Message |
| event cursor | Room sequence |
| outbox ID | Publish ID |

Chat 数据库仍可保存 conversation title、read cursor 和 unread projection。这些字段不进入 Delivery Core 的通用 Room 模型。

### 16.2 WebRTC

```json
{
  "name": "rtc.signal",
  "profile": "ephemeral",
  "expires_at": "2026-08-13T16:00:30Z",
  "data": {
    "call_id": "call-123",
    "to_identity_id": "bob",
    "kind": "ice",
    "candidate": {}
  }
}
```

Delivery Core 路由信令，但不理解 SDP、ICE、CallKit、TURN 或媒体流。业务如需只投递给 Room 内指定成员，可以在 payload validator/command handler 中校验目标成员，再使用定向投递扩展；首版默认广播到 Room。

### 16.3 AI Bot

Bot 使用普通 Identity 和 Member Grants。中间输出使用 Stream，最终回复使用 Durable：

```text
ai.output.delta       stream
ai.output.completed   stream final
message.created       durable final message
```

模型密钥、上下文选择、工具调用和计费都由 AI 业务服务负责。

## 17. 可观测性

核心应暴露结构化指标，而不是让宿主解析日志：

- active connections
- connections per identity
- publish accepted/committed/rejected
- publish latency
- durable recovery events
- cursor gaps
- outbound queue depth
- slow-client disconnects
- dropped ephemeral messages
- stream coalescing count
- Store and Bus errors

日志至少包含 connection ID、identity ID、room ID、publish ID、event ID 和 error code，但默认不记录 `data`。

## 18. 实现状态

Delivery v1 已完成：

1. `delivery` package、核心 types、capabilities 和 JSON envelope。
2. Memory Store/Bus、三路背压和 WebSocket 状态机测试。
3. 复用 Chat 现有表的 PostgreSQL Store adapter，没有建立重复 Room 表。
4. Durable publish、committed ACK、幂等、Room sequence 和 resume。
5. Ephemeral publish，用于 WebRTC signaling。
6. 服务端按 membership 自动路由，成员移除立即撤权。
7. Web 和 iOS 客户端迁移到 `/realtime`。
8. Chat 客户端发布入口只接受 `message.created/durable` 与 `rtc.signal/ephemeral`；业务事实不能由客户端任意伪造。

后续演进项：

1. Stream snapshot、流恢复和 AI Bot 接入。
2. 多实例实际出现时增加外部 Bus adapter。
3. 更完整的 Web optimistic projection；持久 IndexedDB outbox 已在 v1 提供。
4. WebRTC 指定成员定向投递；v1 在 Room 内广播，由 payload 中的目标信息供客户端过滤。
5. 有明确性能证据后再评估 MessagePack 或 Protobuf。
6. 按真实容量需求补充核心 rate limit 与结构化 metrics；v1 先复用应用现有 HTTP 限流和队列背压。

这些演进项不能改变 v1 已定义的 Durable 和 Ephemeral 语义；新增 wire 字段必须保持旧客户端可以忽略。
