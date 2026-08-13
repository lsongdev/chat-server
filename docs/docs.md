# Flame Chat 设计

## 1. 设计原则

Flame 是单机优先的轻量聊天服务。设计只保留四条规则：

1. OIDC 是唯一身份入口，服务端 Session 是唯一 API 凭证。
2. HTTP 是唯一持久写入通道，所有可重试创建都使用客户端 UUID。
3. PostgreSQL 事件流是消息和会话状态的唯一事实来源。
4. WebSocket 只通知“哪里变了”，客户端始终从 HTTP 恢复完整状态。

逐字段报文以 [protocol.md](protocol.md) 为唯一协议规范。

## 2. 架构

```text
Web / iOS
   │
   ├── HTTPS: OIDC session, commands, queries, event history
   └── WSS:   conversation.changed / conversation.deleted
                       │
                  Go chat-server
                       │
                  PostgreSQL
```

Go 服务不托管前端静态文件。开发入口由 Vite 提供 React SPA，并把 `/api`、`/auth`、`/ws` 和健康检查代理到 Go。Cloudflare Tunnel 与所有客户端只访问 `chat.lsong.org`，因此 Cookie、Origin 校验与 WebSocket 保持同源。Docker Compose 只用于提供 PostgreSQL，应用运行方式与数据库部署方式无关。

## 3. 身份与安全

`GET /auth/login` 使用 OIDC Authorization Code、PKCE、state 和 nonce。只有身份提供方返回已验证邮箱时才创建本站 Session。本站保存 opaque Session 哈希，不向客户端暴露数据库或提供方凭证。

- Session Cookie：HttpOnly、SameSite=Lax，HTTPS 环境启用 Secure。
- Mutation：要求允许的 `Origin`。
- Logout：同时撤销数据库 Session 与客户端 Cookie。
- 本地数据：iOS 在账户切换或 Session 失效时清理当前账户缓存，不能跨账户复用 outbox。
- API 密钥：不编译进应用；用户自行配置的第三方密钥不属于 Chat 协议。

## 4. 数据模型

产品只有一种交流容器：`conversation`。私聊和群聊使用同一模型。

- `users`：OIDC 身份与资料。
- `auth_sessions`：本站 Session。
- `conversations`：标题和当前 `last_seq`。
- `conversation_members`：成员角色、加入/离开游标和已读游标。
- `conversation_events`：消息及元数据事件。
- `contacts`：当前账户的服务端同步联系人。

每个 conversation 的事件序号严格递增。消息、改名和成员变化共享序号，因此 `last_seq` 只代表同步游标。未读消息数由服务端仅统计 `message.created`，不通过游标相减推断。

## 5. 写入与幂等

创建 conversation 时客户端提供 conversation UUID；发送消息时提供 `client_message_id`。网络超时后必须复用原 UUID：

```text
client UUID ── POST ──> transaction ──> event seq
     │                                  │
     └──────── retry same UUID ─────────┘ returns original result
```

消息内容只编码一次：文本使用 `{"type":"text","text":"..."}`，其他类型使用 `{"type":"image","data":{...}}` 等结构。数据库的 `payload` 直接保存这份 JSON。

## 6. 实时与同步

WebSocket 不接受业务命令。它只发送 conversation UUID 和最新游标。这样鉴权、校验、限流、错误格式与幂等逻辑只需要在 HTTP 实现一次。

客户端同步流程：

1. 登录后读取一次 `/api/me`。
2. 获取 active conversation 列表，并删除本地已同步但服务端已不存在的记录。
3. 从本地连续游标分页拉取事件。
4. 按 `(conversation_id, seq)` 保存事件，按 `client_message_id` 合并本地 outbox。
5. WebSocket 提示、回到前台、重连或发现序号缺口时重新执行同步。
6. 失败写入使用相同客户端 UUID 指数退避重试。

连接中断不会改变一致性：HTTP 和 PostgreSQL 始终是权威来源，WebSocket 丢包只会延迟刷新。

## 7. 单机边界

当前 Hub 位于单个 Go 进程内，适合单实例部署。PostgreSQL 已保证持久数据一致性。未来扩展多实例时，只需给变更通知增加 Redis Pub/Sub、PostgreSQL `LISTEN/NOTIFY` 或消息总线；HTTP API 和客户端同步协议无需改变。

## 8. 运行约束

- 所有 migration 版本化并在启动时事务执行。
- 请求体限制大小并拒绝未知字段。
- 列表响应返回空数组而不是 `null`。
- 错误统一为 `{ "error": { "code", "message" } }`。
- `healthz` 只表示进程存活；`readyz` 验证数据库可用。
- 日志记录请求 ID、方法、路径和耗时，不记录 Cookie、消息正文或密钥。
