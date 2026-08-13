# Delivery Core

`delivery` 是一个与聊天业务解耦的 Go 实时消息组件。它只认识不透明的
Identity、Room、Member 和 Message，不认识用户邮箱、头像、会话标题、AI
模型或 WebRTC 媒体细节。

应用提供认证和持久化适配器，Delivery Core 提供一个 `/realtime` WebSocket
Handler，并统一处理：

- 按 Room 自动路由，不要求客户端维护 topic/subscription。
- 每次发布时检查当前成员关系和 capability。
- Durable 消息的原子 sequence、幂等 ACK、断线恢复和去重依据。
- Ephemeral 消息的在线投递、TTL 和背压丢弃语义。
- 成员变化后的在线路由刷新。
- 单连接内 control、durable、realtime 三类流量隔离。

## 最小接入

```go
engine, err := delivery.New(delivery.Options{
	Authenticate: func(ctx context.Context, request *http.Request) (delivery.Identity, error) {
		user, ok := currentUser(ctx)
		if !ok {
			return delivery.Identity{}, delivery.ErrPermissionDenied
		}
		return delivery.Identity{ID: user.ID.String()}, nil
	},
	Store: appDeliveryStore,
	OriginCheck: func(request *http.Request) bool {
		return request.Header.Get("Origin") == "https://example.com"
	},
})
if err != nil {
	return err
}
defer engine.Close()

router.Handle("/realtime", engine.Handler())
```

`Authenticate` 和 `Store` 必填。未提供 `Bus` 时使用进程内 `MemoryBus`。
`MemoryStore` 适合测试和示例，不应作为需要持久恢复的生产存储。

宿主应用可以通过 `HandleClientPublish` 限制客户端能发布的事件名和 payload，
或把命令转换成业务事实。认证得到的 Identity、授权 Room 和客户端 publish ID
始终由核心固定，业务处理器不能伪造或改写。

## 模型与权限

- `Identity.ID`：稳定、非空、不透明字符串，格式由宿主应用决定。
- `Room.ID`：授权、排序和恢复的最小边界。
- `Member`：Identity 在 Room 中的持久授权关系。
- `Message.Name`：小写点分隔名称，例如 `message.created`、`rtc.signal`。
- `Message.Data`：核心不解释的 JSON，由宿主业务验证。

Capability 包含：

- `room.receive`
- `message.publish`
- `history.read`
- `members.manage`
- `room.manage`

管理者不能授予自己不具备的 capability。成员被移除后，现有连接会立即失去
Room 路由；发布时仍会重新读取 Store 中的当前权限。

## Delivery profiles

### Durable

Durable 用于不能丢失的最终事实：

1. `Store.Append` 原子分配 Room sequence，并按
   `(room_id, actor_id, publish_id)` 保证幂等。
2. 数据提交后才返回 `status: committed`。
3. 在线成员收到 Event；漏掉的 Event 可用 Room cursor 恢复。
4. 网络层允许重复，客户端按 `(room_id, sequence)` 去重。

这是“至少一次传输 + 幂等写入 + 客户端去重”，不是端到端 exactly-once。

### Ephemeral

Ephemeral 用于 typing、presence、WebRTC signaling 等临时信号：

- 通过认证、授权和校验后返回 `status: accepted`。
- 不写入持久 Store，不分配 durable sequence，也不能恢复。
- 过期或 realtime 队列拥塞时允许丢弃。
- accepted 不表示任何接收端已经处理。

`Stream` 字段已预留，但在 snapshot、合并和恢复语义实现前会被拒绝。

## WebSocket 协议

连接必须协商 subprotocol：

```http
Sec-WebSocket-Protocol: delivery.v1
```

报文是 UTF-8 JSON。文本 frame 是规范形式；服务端也兼容内容为 UTF-8 JSON
的 binary frame，方便原生客户端迁移。

连接建立后，服务端首先发送：

```json
{"op":"hello","protocol":"delivery.v1","connection_id":"...","identity_id":"alice","max_message_bytes":65536}
```

客户端提交每个本地 Room 的最后连续 sequence：

```json
{"op":"resume","rooms":{"room-1":41}}
```

发布 Durable 消息：

```json
{"op":"publish","id":"publish-1","room_id":"room-1","name":"message.created","profile":"durable","data":{"type":"text","text":"hello"}}
```

提交成功：

```json
{"op":"ack","id":"publish-1","status":"committed","event_id":"event-1","sequence":42}
```

Room 成员收到：

```json
{"op":"event","room_id":"room-1","id":"event-1","publish_id":"publish-1","name":"message.created","profile":"durable","sequence":42,"actor_id":"alice","data":{"type":"text","text":"hello"},"created_at":"2026-08-13T16:00:00Z"}
```

恢复数据位于 `sync.begin` 和 `sync.end` 之间，Event 带有
`"recovered": true`。成员路由变化使用 `room.added` 和 `room.removed`。

## Store 与 Bus

`Store` 是 Durable 数据的唯一事实来源。实现必须保证：

- `Append` 中 sequence 分配、事件写入和 publish 幂等是一个原子操作。
- `EventsAfter` 严格按 sequence 升序返回。
- `Member` 和 `RoomsForIdentity` 反映当前有效授权。
- `HistoryStart` 限制新成员可以恢复的最早历史。

`Bus` 只负责跨连接在线 fanout，不是持久事实来源。单实例使用 `MemoryBus`；
多实例可实现 NATS、MQTT、Redis 或 PostgreSQL adapter，而客户端协议无需变化。

宿主业务在自己的事务中写入 Durable Event 后，可以调用
`Engine.NotifyCommitted` 进行 fanout；成员关系在业务事务中变化后调用
`Engine.RefreshIdentity`。删除 Room 后调用 `Engine.RemoveRoomRouting`。

## 背压和恢复

- control 队列：hello、ACK、error、Room 路由变化。
- durable 队列：持久 Event 和 sync 边界；溢出时关闭慢连接，由 cursor 恢复。
- realtime 队列：Ephemeral；溢出时允许丢弃。

客户端应先把 Durable publish 写入本地 outbox，再发送；收到 committed ACK 后删除。
连接中断或 retryable error 后，必须使用同一个 publish ID 重发。

完整设计和 Flame Chat 映射见
[`../docs/delivery-core-design.md`](../docs/delivery-core-design.md)，线上 wire contract
见 [`../docs/protocol.md`](../docs/protocol.md)。
