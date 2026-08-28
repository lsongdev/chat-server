# Delivery / Messaging / Chat 分层设计

> 状态：Implemented

## 1. 结论

系统拆成三个单向依赖层：

```text
Chat Server
  conversation / role / contact / message schema / WebRTC rules
                         |
                         v
Messaging framework
  room / member / capability / grants / history boundary
                         |
                         v
Delivery kernel
  connection / publish / event / sequence / cursor / bus / backpressure
```

底层提供机制，中层提供通用消息系统策略，顶层提供 Chat 产品语义。
依赖只能向下，Delivery 不导入 Messaging 或 Chat。

## 2. Delivery kernel

Delivery 只认识：

```text
Identity ID
Routing scope ID (wire 中仍叫 room_id)
Publish
Event
Sequence
Cursor
Connection
Store
Bus
```

它不定义 Member、Role、Capability、Grants，也不创建或删除 Room。

核心流程：

```text
connect(identity)
  -> Access.Routes(identity)
  -> register socket routes

publish(scope, stable ID)
  -> Access.CanPublish
  -> validate envelope
  -> Store.Append (durable)
  -> ACK after commit
  -> Bus -> local Hub

resume(scope, cursor)
  -> Access.HistoryStart
  -> fixed HeadSequence snapshot
  -> EventsAfter pages up to head
```

`Access` 是一个窄端口，不是底层领域模型。它只回答投递机制必须知道的问题。

### 必须保留的可靠性性质

1. 稳定 publish ID 与原子幂等写入。
2. 每个 scope/Room 独立且连续的 sequence。
3. Store 是正确性来源，Hub 只优化延迟。
4. 恢复先读取固定 head；恢复期间更晚的事件走 realtime，客户端按 sequence 去重。
5. Bus publish 失败不撤销已提交事件，cursor replay 可修复漏推。
6. Durable 背压关闭连接，Ephemeral 背压允许丢弃。

## 3. Messaging framework

Messaging 拥有通用消息系统概念：

```text
Room
Member
Capability
Grants
HistoryStart
```

它提供 Room/Member 生命周期与授权，并通过内部 adapter 实现 Delivery 的
`Access` 和事件 `Store`。Capability 包含：

- `room.receive`
- `message.publish`
- `history.read`
- `members.manage`
- `room.manage`

这些能力不再出现在 Delivery 包中。未来另一个协作或游戏应用可以复用
Messaging；只需要实现自己的 `messaging.Store`。

## 4. Chat Server

Chat 拥有：

- conversation 的 title、状态和产品生命周期；
- owner/admin/member 角色以及最后一个 owner 等约束；
- contacts、OIDC session、头像、已读/未读；
- `message.created`、`rtc.signal` 等事件 schema；
- PostgreSQL 表与事务。

`ChatMessagingStore` 把 Chat 数据投影为 `messaging.Store`：

```text
conversations          -> messaging.Room
conversation_members   -> messaging.Member + Grants
conversation_events    -> delivery.Event log
```

Chat 的 HTTP mutation 在自己的事务里创建事件，然后调用
`messaging.Engine.Broadcast`；成员变化后调用 `RefreshIdentity`；Room 删除后调用
`InvalidateRoom`。

## 5. API 所有权

| 能力 | 所有者 |
|---|---|
| WebSocket、ACK、resume、Hub、背压 | Delivery |
| Event append、sequence、cursor | Delivery port / Store implementation |
| Room/Member/Capability/Grants | Messaging |
| conversation/role/contact/schema | Chat |
| Bus 连接生命周期 | 调用方 |

这种边界允许独立演进：替换 NATS/Redis/PG Bus 不影响 Messaging 和 Chat；改变
Chat 角色规则不影响 Delivery；未来若出现另一种消息产品，也不需要复制可靠投递内核。
