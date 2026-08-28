# Delivery kernel

`delivery` 是最底层的消息投递内核。它只负责连接、事件、顺序、游标恢复、
在线 fanout 和背压，不拥有 Room/Member/Role/Capability 的领域模型。

内核只依赖三个宿主端口：

```go
type Access interface {
	Routes(context.Context, string) ([]string, error)
	CanPublish(context.Context, string, string) error
	HistoryStart(context.Context, string, string) (int64, error)
}

type Store interface {
	Append(context.Context, Publish) (Event, error)
	EventsAfter(context.Context, string, int64, int) ([]Event, error)
	HeadSequence(context.Context, string) (int64, error)
}

type Bus interface {
	Publish(context.Context, Event) error
	Subscribe(func(Event)) (func(), error)
}
```

`Access` 不是内核权限模型，只是路由所需的窄端口。仓库中的
[`messaging`](../messaging) 包实现这个端口，并在上一层提供 Room、Member 和
Capability。

## 保证

- Durable publish 在 Store commit 后才 ACK。
- Store 按 Room 分配连续 sequence，并用稳定 publish ID 保证幂等。
- 在线事件由 Bus 和 Hub 推送；fanout 失败不撤销已经提交的事实。
- 断线通过固定 head snapshot 与 cursor replay 恢复。
- Durable 队列溢出时关闭连接并依靠恢复补齐；Ephemeral 可以丢弃。
- Bus 生命周期属于调用方；Engine 关闭时只取消订阅和关闭自己的连接。

WebSocket wire contract 仍为 `delivery.v1`，详见
[`../docs/protocol.md`](../docs/protocol.md)。整体三层设计见
[`../docs/delivery-core-design.md`](../docs/delivery-core-design.md)。
