# Messaging framework

`messaging` 构建在 `delivery` 之上，提供可复用的消息系统领域模型：

- Room 与 Member 生命周期；
- Capability/Grants 授权；
- 成员历史起点；
- Room 路由投影；
- 对底层 Publish/Event/Bus 的稳定封装。

应用提供一个 `messaging.Store`。框架把成员能力翻译成底层内核需要的三个问题：
当前 Identity 有哪些 Room、能否发布、最早可以从哪个 sequence 恢复。

```go
engine, err := messaging.New(messaging.Options{
	Authenticate: authenticate,
	Store:        store,
})
```

`messaging.NewMemoryStore()` 提供完整的内存 Room/Member/Event 实现；底层
`delivery.NewMemoryStore()` 则刻意只实现事件日志。

Chat Server 可以把 `conversations` 和 `conversation_members` 投影为 Room/Member，
同时继续在 Chat 层拥有 title、owner/admin/member 角色约束、联系人、消息 schema
和 WebRTC 规则。这样 Messaging 是可复用框架，但 Delivery 内核不被这些策略污染。
