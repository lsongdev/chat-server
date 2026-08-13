# Flame Chat 设计

## 1. 设计原则

Flame 是单机优先的轻量聊天服务。设计只保留四条规则：

1. OIDC 是唯一身份入口，服务端 Session 是唯一 API 凭证。
2. Delivery WebSocket 统一承担消息发布和实时投递，所有可重试发布都使用客户端 UUID。
3. PostgreSQL 事件流是消息和会话状态的唯一事实来源。
4. 客户端用 Room cursor 通过同一连接恢复持久事件；HTTP 保留业务命令、查询和历史兜底。

逐字段报文以 [protocol.md](protocol.md) 为唯一协议规范。

## 2. 架构

```text
Web / iOS
   │
   ├── HTTPS: OIDC session, business commands, queries, history fallback
   └── WSS:   Delivery v1 publish / ack / event / resume
                       │
                  Go chat-server
                       │
                  PostgreSQL
```

Go 服务不托管前端静态文件。开发入口由 Vite 提供 React SPA，并把 `/api`、`/auth`、`/realtime` 和健康检查代理到 Go。Cloudflare Tunnel 与所有客户端只访问 `chat.lsong.org`，因此 Cookie、Origin 校验与 WebSocket 保持同源。Docker Compose 只用于提供 PostgreSQL，应用运行方式与数据库部署方式无关。

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

## 5. 投递与幂等

创建 conversation 时客户端提供 conversation UUID；发布消息时提供 publish UUID。网络中断后 SDK 必须复用原 UUID：

```text
client UUID ── publish ──> transaction ──> event seq ──> committed ACK
     │                                         │
     └──────── reconnect + same UUID ──────────┘ returns original result
```

消息内容只编码一次：文本使用 `{"type":"text","text":"..."}`，其他类型使用 `{"type":"image","data":{...}}` 等结构。数据库的 `payload` 直接保存这份 JSON。

## 6. Delivery Core 与同步

独立 `delivery` package 只认识 Identity、Room、Member、Capability、Message 和 Delivery。Chat 通过 Store adapter 将现有 conversation 表映射为 Room，不建立重复数据。角色由业务层映射为 capabilities，核心负责每次发布时验证当前 membership。

客户端同步流程：

1. 登录后读取一次 `/api/me`。
2. 获取 active conversation 列表，并删除本地已同步但服务端已不存在的记录。
3. `/realtime` hello 后提交每个 Room 的本地连续游标。
4. 按 `(room_id, sequence)` 保存 Durable Event，按 `publish_id` 合并本地 outbox。
5. 未收到 ACK 的消息由 SDK 在重连后复用 UUID 发布；页面不拥有 retry timer。
6. Ephemeral Event 不写数据库、不占 durable cursor，用于 WebRTC signaling 等短期信号。
7. 回到前台或发现序号缺口时，通过 HTTP history 执行兜底同步。

连接中断不会改变一致性：HTTP 和 PostgreSQL 始终是权威来源，WebSocket 丢包只会延迟刷新。

## 7. 单机边界

Delivery Core 默认使用进程内 Memory Bus，适合单实例部署。PostgreSQL 已保证持久数据一致性。未来扩展多实例时只需实现 NATS、MQTT、Redis 或 PostgreSQL Bus adapter；Delivery 协议和客户端无需改变。

## 8. 运行约束

- 所有 migration 版本化并在启动时事务执行。
- 请求体限制大小并拒绝未知字段。
- 列表响应返回空数组而不是 `null`。
- 错误统一为 `{ "error": { "code", "message" } }`。
- `healthz` 只表示进程存活；`readyz` 验证数据库可用。
- 日志记录请求 ID、方法、路径和耗时，不记录 Cookie、消息正文或密钥。
