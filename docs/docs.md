# 轻量级聊天服务器设计（V1）

状态：设计基线

部署范围：单机、独立前端容器、单个 Go 后端进程、单个 PostgreSQL 实例

身份来源：所有客户端均可使用姓名+邮箱；Web 可选用 `https://my.lsong.org` OIDC 获取邮箱

更新时间：2026-08-10

## 1. 结论

V1 采用以下组合：

- Go HTTP/WebSocket 服务；
- React/Vite 前端独立构建，由 Nginx 容器提供静态文件和同源反向代理；
- PostgreSQL 作为唯一持久化数据库；
- 进程内 Hub 完成在线消息分发；
- 所有客户端都可以通过姓名和规范化邮箱登录，Web 也可通过 OIDC Authorization Code Flow 获取同样的资料；
- 客户端使用会话内单调递增的 `seq` 同步历史和补齐离线消息；
- 暂不引入 Redis、Kafka、NATS、独立用户系统或微服务。

SQLite 在单机聊天服务中并非不可用，但本项目不采用它。原因不是容量或文件大小，而是聊天属于持续写入且可能突发的负载：SQLite 即使启用 WAL，同一时刻仍只有一个 writer。用户量增加后，消息写入、成员变更和已读游标会争用同一个写锁。PostgreSQL 可以继续保持单机部署，同时避免在业务增长后迁移数据库和重写 SQL。

## 2. 目标与非目标

### 2.1 V1 目标

- 用户只需姓名和邮箱即可聊天，本站不保存密码；
- 用户可以创建会话、添加或移除成员；
- 账户、成员、搜索结果与消息使用邮件地址对应的 Gravatar 头像；
- 产品中只有会话这一种交流容器；
- 在线成员通过 WebSocket 实时收到消息；
- 离线或网络抖动后能够可靠补齐消息；
- 同一个发送请求可以安全重试，不产生重复消息；
- 单机进程重启不会丢失已确认消息。

### 2.2 V1 非目标

- 多机部署和跨节点实时分发；
- 端到端加密；
- 音视频通话；
- 超大直播间或百万成员广播；
- 全文检索、复杂消息审核和多区域容灾；
- 自建注册、密码、找回密码或 MFA 系统；
- 精确记录每条消息对每台设备的投递状态。

## 3. 总体架构

```text
                       +-----------------------+
                       | my.lsong.org          |
                       | OIDC Provider         |
                       +-----------+-----------+
                                   |
                            Authorization Code
                                   |
+-------------+       HTTPS / WebSocket       +------------------+
| Web Client  | <----------------------------> | Frontend Nginx   |
| local cache |                                | React SPA        |
+-------------+                                +--------+---------+
                                                        |
                                      /api /auth /ws    | Compose network
                                                        v
                                               +--------+---------+
                                               | Go Chat Backend  |
                                               | HTTP API         |
                                               | Auth/session     |
                                               | In-memory Hub    |
                                               +--------+---------+
                                                        |
                                                        | SQL
                                                        v
                                               +--------+---------+
                                               | PostgreSQL       |
                                               +------------------+
```

PostgreSQL 是消息事实来源，WebSocket 只是低延迟通知通道。只有数据库事务提交成功后，服务器才能确认消息已经发送成功。

前后端在源码、依赖、镜像和运行进程上完全分离。Go 后端不包含 `frontend/dist`，也不处理 `/chat`、`/contacts` 等浏览器路由。生产入口由前端 Nginx 统一暴露：静态路由采用 SPA fallback，协议路由转发给后端。这个边界允许两端独立构建和替换，同时保持单域名，避免为了跨站 Cookie 与 CORS 引入额外复杂度。

单机版不需要 Redis：所有 WebSocket 都在同一个 Go 进程内，Hub 可以直接按会话广播。如果进程在数据库提交后、WebSocket 广播前崩溃，客户端重连时会按 `seq` 从 PostgreSQL 补齐消息。

## 4. 登录与 OIDC 接入设计

原生客户端和 Web 登录页都调用 `POST /auth/email`，只提交 `name` 和 `email`。服务端规范化邮箱、创建或更新用户并签发本站 opaque session。Web 还可选择 OIDC，回调得到邮箱后也落到同一个用户；已有邮箱匹配必须唯一，否则拒绝自动归并。OIDC 是获取邮箱资料的可选手段，不是 Chat 身份模型本身。

该简易流程不证明调用者拥有邮箱，只适用于受信环境或早期产品。公开部署应增加邮件验证码或在可信身份网关后开放。

### 4.1 已确认的 Provider 能力

从 `https://my.lsong.org/.well-known/openid-configuration` 获取到：

| 配置 | 值 |
| --- | --- |
| Issuer | `https://my.lsong.org` |
| Authorization endpoint | `https://my.lsong.org/oauth/authorize` |
| Token endpoint | `https://my.lsong.org/oauth/token` |
| UserInfo endpoint | `https://my.lsong.org/oauth/userinfo` |
| JWKS URI | `https://my.lsong.org/.well-known/jwks.json` |
| Revocation endpoint | `https://my.lsong.org/oauth/revoke` |
| Flow | `authorization_code` |
| PKCE | `S256` |
| ID token algorithm | `ES256` |
| Scopes | `openid profile email offline_access` |
| Identity claims | `sub name preferred_username picture email email_verified` |

Discovery 文档没有声明 `registration_endpoint`，因此 V1 假定在用户中心预先创建 confidential client，并配置精确的 redirect URI。推荐值：

```text
开发：http://localhost:8080/auth/callback
生产：https://<chat-domain>/auth/callback
```

生产环境必须使用 HTTPS，redirect URI 不允许使用通配符。

### 4.2 默认 Name + Email 登录流程

```text
Client                  Chat Server                 PostgreSQL
   | POST /auth/email       |                           |
   | {name,email}           |                           |
   |----------------------->| 规范化 email              |
   |                        | 按 email 创建/更新用户     |
   |                        |-------------------------->|
   | Set-Cookie + user      |                           |
   |<-----------------------|                           |
```

Web 未登录访问 `/` 时直接显示姓名和邮箱表单；邀请链接会把原路径放入 `return_to`，登录成功后继续加入流程。

### 4.3 可选 OIDC 登录流程

```text
Browser                 Chat Server                 my.lsong.org
   | GET /auth/login         |                            |
   |------------------------>|                            |
   |                         | 生成 state/nonce/verifier  |
   | 302 /oauth/authorize    |                            |
   |<------------------------|                            |
   |----------------------------------------------------->|
   |                   用户登录并授权                     |
   |<-----------------------------------------------------|
   | GET /auth/callback?code=...&state=...                |
   |------------------------>|                            |
   |                         | POST /oauth/token + verifier
   |                         |--------------------------->|
   |                         |<---------------------------|
   |                         | 校验 ID token              |
   |                         | JIT 创建/更新本地用户       |
   |                         | 创建本站 session           |
   | Set-Cookie + 302 /      |                            |
   |<------------------------|                            |
```

必须实现以下校验：

- Authorization Code Flow 同时使用 PKCE S256；
- `state` 防 CSRF，`nonce` 绑定本次登录；
- 使用 Discovery/JWKS 校验 ES256 签名；
- 严格校验 `iss == https://my.lsong.org`；
- 校验 `aud` 包含本站 `client_id`，以及 `exp`、`iat`、`nonce`；
- OIDC `sub` 只标识登录提供方身份；聊天业务跨端以规范化 email 唯一识别用户，数据库仍以 UUID 作为外键；
- 如果业务要求必须有可信邮箱，则要求 `email_verified == true`；
- OAuth/OIDC 错误只记录必要字段，禁止记录 code、token、client secret。

建议使用成熟的 Go OIDC/OAuth2 库完成 Discovery、PKCE 和 token 校验，不自行实现 JWT 验签。

### 4.3 本地用户与 Session

用户中心负责身份认证，聊天服务仍需要一张本地影子用户表，用于外键、昵称快照和站内封禁：

```sql
CREATE TABLE users (
    id                 uuid PRIMARY KEY,
    oidc_issuer        text NOT NULL,
    oidc_subject       text NOT NULL,
    username           text,
    display_name       text,
    email              text NOT NULL,
    email_verified     boolean NOT NULL DEFAULT false,
    picture_url        text,
    status             text NOT NULL DEFAULT 'active',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    last_login_at      timestamptz,
    UNIQUE (oidc_issuer, oidc_subject)
);
```

首次登录 JIT 创建用户，以后登录更新可变 profile 字段。会话成员、消息发送者等内部外键都引用 `users.id`，不要直接引用 email 或 username。

邮件地址是跨客户端的唯一业务身份，数据库 UUID 只是内部主键和外键。`lower(email)` 有唯一索引；所有入口先去除空白并转小写。成员管理只接受完整邮件地址并做大小写不敏感的精确匹配，不提供前缀或模糊搜索。升级旧数据库前必须先处理缺失或重复邮箱，否则唯一身份 migration 会拒绝执行。

界面头像使用 Gravatar。服务端将邮件地址去除首尾空白并转换为小写后计算 SHA-256，只向客户端返回 `https://gravatar.com/avatar/<hash>` URL，不把原始邮件地址发送给 Gravatar。请求固定使用 `rating=g`、96 像素和 `identicon` 默认头像；页面上的头像图片设置 `referrerpolicy=no-referrer`。没有邮件地址的账户使用空字符串哈希，因此仍能获得稳定的默认头像。数据库中的 `picture_url` 暂时保留以兼容 OIDC profile，但 V1 界面不使用它。

浏览器不直接把 Provider access token 当作本站长期 Cookie。回调成功后创建本站 opaque session：

```sql
CREATE TABLE auth_sessions (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  bytea NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    user_agent  text,
    ip          inet
);
```

- Cookie 名称建议 `__Host-chat_session`；
- 使用至少 256 bit CSPRNG 生成原始 token，数据库只保存 SHA-256 hash；
- Cookie 设置 `Secure; HttpOnly; SameSite=Lax; Path=/`，不能设置 `Domain`；
- V1 session 建议绝对有效期 24 小时，可按需求增加有限的滑动续期；
- `/auth/logout` 删除本地 session 并清 Cookie；
- Provider 没有在 Discovery 中声明 `end_session_endpoint`，所以不能假设本地登出会同时结束用户中心 SSO；
- V1 不申请 `offline_access`，也不保存 refresh token。聊天服务只需要确认身份，不需要长期代表用户调用用户中心 API。

短期登录事务保存在 `oidc_login_attempts` 表中，记录 state hash、nonce、PKCE verifier 和 10 分钟过期时间，并由后台定期清理。

### 4.4 WebSocket 认证

- 浏览器建立 `/ws` 时携带本站 session Cookie；
- Upgrade 前查询 `auth_sessions` 并将内部 `user_id` 固定到连接上下文；
- 严格校验 `Origin` 在允许列表内；
- 不接受 URL query 中的 bearer token，避免 token 出现在日志和历史记录；
- 每次发送消息仍需检查用户当前是否为会话成员；
- 本地封禁后主动关闭该用户的所有连接；
- 每条连接必须有有界发送队列，慢客户端队列满时断开，让它重连后从数据库同步。

## 5. PostgreSQL 数据模型

以下是核心逻辑模型，实际约束和索引以 `migrations/` 中的版本化文件为准；服务启动时只执行尚未记录在 `schema_migrations` 中的 migration。

```sql
CREATE TABLE conversations (
    id          uuid PRIMARY KEY,
    title       text,
    created_by  uuid NOT NULL REFERENCES users(id),
    last_seq    bigint NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE conversation_members (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            text NOT NULL DEFAULT 'member'
                    CHECK (role IN ('owner', 'admin', 'member')),
    joined_seq      bigint NOT NULL DEFAULT 0,
    left_seq        bigint,
    last_read_seq   bigint NOT NULL DEFAULT 0,
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'left', 'removed')),
    joined_at       timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, user_id)
);

CREATE TABLE conversation_member_periods (
    id              uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_seq      bigint NOT NULL,
    left_seq        bigint,
    leave_reason    text
);

CREATE TABLE conversation_events (
    conversation_id  uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    seq              bigint NOT NULL,
    id               uuid NOT NULL UNIQUE,
    sender_id        uuid REFERENCES users(id),
    client_event_id  uuid,
    event_type       text NOT NULL,
    payload          jsonb NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, seq),
    UNIQUE (conversation_id, sender_id, client_event_id)
);

CREATE INDEX conversation_members_user_active_idx
    ON conversation_members (user_id, conversation_id)
    WHERE status = 'active';

CREATE INDEX conversations_updated_idx
    ON conversations (updated_at DESC);

CREATE INDEX conversation_events_sender_idx
    ON conversation_events (sender_id, created_at DESC);
```

约束说明：

- 一条消息在会话日志中只保存一次，不为每个收件人复制；
- `seq` 只保证会话内顺序，不承诺跨会话全局顺序；
- 消息、成员加入/退出/移除、改名共享 `conversation_events` 和同一条 `seq`；
- `client_message_id` 映射为消息事件的 `client_event_id`，网络超时重试时保持不变；
- `conversation_member_periods` 保存多次加入/退出区间；
- 所有会话使用相同的数据结构，成员数量从一人到配置上限；
- 用户通过完整邮件地址精确查找并加入会话，服务端内部仍使用不可变的 `users.id` 建立成员关系；
- 附件内容不进入数据库；V1 若加入附件，只保存元数据和受控 URL；
- 用户离开后的历史可见性由 `joined_seq` 和成员状态控制。V1 重新加入时从新的 `joined_seq` 开始，不恢复离开期间的访问权。

## 6. 写入、顺序和幂等

发送消息必须在一个短事务中完成：

```text
BEGIN
  1. 校验 sender 是 active member
  2. UPDATE conversations
       SET last_seq = last_seq + 1, updated_at = now()
       WHERE id = $conversation_id
       RETURNING last_seq
  3. INSERT conversation_events(..., seq = returned_last_seq, ...)
COMMIT
  4. 返回 stored ack
  5. 发布到进程内 Hub
```

同一会话的并发写入会在 `conversations` 对应行上串行化，因此得到严格的会话内顺序。不同会话可以并发写入。

唯一约束保证同一个 `client_message_id` 重试不会重复插入。发生冲突时，服务端查询并返回原消息。接口语义是“至少一次请求 + 幂等去重”，不宣称端到端 exactly-once。

服务端确认分为：

- `stored`：事务已提交，是发送接口成功的含义；
- `received`：设备已经同步到某个连续 `seq`，V1 主要保存在客户端；
- `read`：用户已经读到 `last_read_seq`，服务端按用户/会话保存最高值。

`last_read_seq` 更新使用 `GREATEST(last_read_seq, $seq)`，客户端每 1～3 秒或离开会话时合并上报，避免每读一条消息写一次数据库。

## 7. 实时 Hub

进程内结构：

```go
type Hub struct {
    // 真实实现只能由 Hub event loop 修改，或使用明确的锁策略。
    byUser         map[UserID]map[*Conn]struct{}
    byConversation map[ConversationID]map[*Conn]struct{}
}
```

连接建立后加载该用户的 active memberships，并加入对应集合。成员变更时同步更新 Hub。发布一条会话消息时，Hub 遍历该会话在本机的连接集合，每个连接最多入队一次。

可靠性边界：

- Hub 不保存历史，也不负责离线消息；
- 数据库提交失败时绝不能广播；
- 数据库提交成功、广播失败时，客户端依靠 `seq` 补齐；
- 进程退出时连接断开，客户端使用指数退避重连；
- 发送队列必须有容量上限，禁止慢客户端拖垮整个进程。

## 8. 客户端协议与离线同步

### 8.1 最小 API

```text
GET  /auth/login
GET  /auth/callback
POST /auth/logout
GET  /api/me
GET  /api/users/search?email=name@example.com

GET  /api/conversations
POST /api/conversations
PATCH /api/conversations/{id}
DELETE /api/conversations/{id}                         # owner 删除整个会话
POST /api/conversations/{id}/leave                     # 当前成员退出
GET  /api/conversations/{id}/members
POST /api/conversations/{id}/members
PATCH /api/conversations/{id}/members/{userID}         # owner 修改角色
DELETE /api/conversations/{id}/members/{userID}
GET  /api/contacts
POST /api/contacts
PUT  /api/contacts/{contactID}
DELETE /api/contacts/{contactID}
GET  /api/conversations/{id}/events?after_seq=123&limit=100
POST /api/conversations/{id}/messages
POST /api/conversations/{id}/read
POST /api/conversations/{id}/invites
POST /api/invites/{token}/accept

GET  /ws
```

所有 mutation 都使用明确的 JSON Content-Type、请求体大小上限和 CSRF 防护。所有会话 API 在 SQL 查询中包含成员授权条件，不能只依赖客户端隐藏入口。

### 8.2 WebSocket 消息示例

客户端发送：

```json
{
  "type": "message.send",
  "request_id": "request-uuid",
  "conversation_id": "conversation-uuid",
  "client_message_id": "stable-message-uuid",
  "content": { "text": "hello" }
}
```

持久化确认：

```json
{
  "type": "message.stored",
  "request_id": "request-uuid",
  "conversation_id": "conversation-uuid",
  "seq": 124,
  "message_id": "message-uuid"
}
```

广播事件：

```json
{
  "type": "conversation.event",
  "event": {
    "type": "message.created",
    "conversation_id": "conversation-uuid",
    "seq": 124,
    "payload": { "text": "hello" }
  }
}
```

服务端和客户端都按 `(conversation_id, seq)` 去重。协议增加 `protocol_version` 或在握手欢迎消息中返回版本，遇到未知 event type 时客户端应忽略而不是断开。

### 8.3 重连和离线消息

客户端本地持久化：

```text
conversation_id -> last_contiguous_seq
```

重连流程：

1. 先建立 WebSocket；
2. 获取会话列表及各会话 `last_seq`；
3. 对 `local_seq < last_seq` 的会话分页请求 `after_seq`；
4. 同步期间继续接收 WebSocket 事件；
5. 按 `seq` 合并、去重；
6. 如果收到的 `seq > local_seq + 1`，立即请求缺失区间；
7. 只有连续区间完整后才推进 `last_contiguous_seq`。

优先同步最近活跃的会话，旧会话在用户打开时再懒加载。这样用户加入大量会话时也不会在每次连接后拉取全部历史。

## 9. SQLite 容量评估与数据库决策

### 9.1 SQLite 能支持什么

SQLite 的理论数据库大小并不是问题：官方当前默认页数上限配合默认 4 KiB page 大约是 17.5 TB，使用最大 page size 的理论上限约 281 TB。实际限制通常先来自磁盘、备份时间和运维，而不是行数。

WAL 模式允许 reader 与 writer 并行，但官方文档明确说明只有一个 WAL 文件，因此同一时刻只能有一个 writer。聊天消息通常至少包含“递增会话序号 + 插入消息”两个写操作；成员和已读状态也会产生写入，所以峰值延迟取决于所有短事务排队时间，而不是注册用户总数。

不能用“多少用户”直接衡量 SQLite 容量：

- 一百万注册用户但只有少量活跃写入，负载可能很低；
- 一千名在线用户在热门群里持续发言，写锁可能先成为瓶颈；
- WebSocket 空闲连接主要消耗 Go 进程内存和文件描述符，与 SQLite writer 数无直接关系。

在现代本地 SSD、WAL、短事务、单进程并且合并已读写入的条件下，可将下面的数值当作工程准入区间，而不是 SQLite 保证：

| 峰值业务写入 | SQLite 判断 |
| --- | --- |
| 不超过约 100 条消息/秒 | 通常有较大余量，仍需压测 |
| 100～500 条消息/秒 | 可能可用，但必须测 p95/p99、checkpoint 和 `SQLITE_BUSY` |
| 持续超过 500 条消息/秒或突发更高 | 不建议作为无需迁移的长期方案 |
| 多个服务进程、容器或主机同时写 | 放弃 SQLite |

这些数值只是保守的项目决策阈值；实际吞吐会随磁盘 fsync、事务大小、`synchronous`、索引和消息尺寸产生数量级差异。SQLite 官方也将高写入并发和需要多服务器的网站列为更适合 client/server 数据库的场景。

### 9.2 最终选择 PostgreSQL

本项目的目标是“实现轻量但用户规模不被设计锁死”。因此 V1 直接使用 PostgreSQL：

- 仍然可以与 Go 服务部署在同一台机器；
- 不改变单机、无 Redis、无微服务的简单拓扑；
- 不需要以后迁移 schema、占位符、类型、migration 和备份流程；
- 不同会话写入可以真正并发；
- 备份、约束、在线 schema migration 和故障诊断更成熟。

SQLite 仍适合作为客户端本地消息缓存或测试中的临时存储，但不作为服务端生产数据库，也不维护 SQLite/PostgreSQL 双兼容层。

## 10. 单机容量规划

容量由三个独立维度决定，应分别压测：

1. WebSocket 连接数：受内存、文件描述符、心跳频率和网络带宽限制；
2. 消息写入率：受 PostgreSQL WAL、磁盘 fsync、热点会话行锁和索引限制；
3. 在线广播量：约等于 `每秒消息数 × 每条消息的在线接收连接数 × 消息字节数`。

V1 不给出脱离硬件的“最大用户数”承诺。建议发布准入目标为：

- 10,000 个持续 WebSocket 连接；
- 500 条消息/秒的混合会话写入；
- 95% 消息小于 4 KiB；
- 正常负载下 stored ack p95 小于 100 ms；
- 广播到在线客户端 p95 小于 200 ms；
- 模拟断线、重启后消息完整且无不可修复序号缺口。

这是一组首轮压测目标，不是架构上限。达到后根据实际机器资源逐步提高，记录 CPU、RSS、网络、数据库连接池、WAL 写入、锁等待和 p99。

一个常见的单机会先遇到广播带宽而不是数据库瓶颈。例如 1 KiB 消息以 100 条/秒发送、每条平均有 1,000 个在线接收者，仅消息正文的下行流量就约 100 MiB/s，尚未计算 JSON、TLS 和 TCP 开销。大群必须限制消息大小、发送频率，并对单连接实施 backpressure。

## 11. 运行与配置

建议环境变量：

```text
CHAT_BASE_URL=https://chat.example.com
HTTP_ADDR=:8080
DATABASE_URL=postgres://chat:...@127.0.0.1:5432/chat?sslmode=...

OIDC_ISSUER=https://my.lsong.org
OIDC_CLIENT_ID=...
OIDC_CLIENT_SECRET=...
OIDC_REDIRECT_URL=https://chat.example.com/auth/callback

SESSION_TTL=24h
ALLOWED_ORIGINS=https://chat.example.com
MAX_MESSAGE_BYTES=8192
MAX_CONVERSATION_MEMBERS=1000
TRUST_PROXY_HEADERS=false
```

要求：

- secrets 只通过环境变量或 secret file 注入，不写入仓库；
- PostgreSQL 只监听 loopback 或受控私网；
- 应用启动时执行连接检查和版本化 migration；单机 V1 不存在多实例 migration 竞争；
- migration 必须可重复追踪，禁止在 handler 中创建或修改 schema；
- 连接池从较小值开始，例如 10～20 个连接，再根据数据库等待指标调整；
- 使用结构化日志且不记录消息正文、Cookie、Authorization header 或 OIDC token；
- 每日 PostgreSQL 逻辑或物理备份，并定期做恢复演练；
- 先优雅停止接收新请求，再关闭 WebSocket，最后关闭数据库连接。

## 12. 安全边界

- 每次读取和发送消息都校验成员状态；
- 服务端从 session 获取 sender，绝不接受客户端提交 sender ID；
- 限制单条文本、JSON 深度、会话成员数、分页大小和 WebSocket frame 大小；
- 登录按 IP 限速，HTTP mutation 按用户限速，WebSocket 发消息按连接限速；
- 会话创建和成员管理要求相应 role；
- 所有时间由服务端生成；
- 使用参数化 SQL；
- 错误响应不暴露 SQL 或 token 校验细节；只有用户主动提交完整邮件地址的精确查找接口会返回是否匹配；
- 登录留有 session 记录，成员和管理变化进入持久化会话事件；
- Cookie session 的普通 HTTP mutation 严格校验 `Origin`；
- 服务端定期 ping，客户端必须 pong，清理失效连接。

## 13. 实现状态

仓库已经实现 PostgreSQL migration、OIDC Discovery/PKCE 登录、本地 opaque session、统一会话、按完整邮件地址精确添加成员、邀请链接、成员生命周期、统一事件序列、幂等发送、进程内 Hub、WebSocket 有界队列、`after_seq` 补偿、已读游标、基础限速、独立前后端 Docker 镜像和 Compose 单机部署。

自动化验证覆盖纯单元测试、race detector、真实 PostgreSQL 17 的会话生命周期、模拟 OIDC Provider 的完整回调流程和真实 WebSocket 消息持久化/广播。发布生产环境前仍应在目标机器执行 10k 连接与混合写入压测、备份恢复演练和实际 `my.lsong.org` client 登录验收。

## 14. 未来多机升级路径

V1 的持久化模型和客户端协议无需修改。升级时：

- 在多个 WebSocket Gateway 之间加入 Redis Sharded Pub/Sub；
- Redis 只发布实时事件，不保存聊天历史；
- 使用 Transactional Outbox 解决 PostgreSQL 提交与 Redis 发布之间的一致性；
- 每个 Gateway 动态订阅本机存在在线成员的会话；
- 客户端继续用同一个 `seq` 协议补偿 Pub/Sub 丢失事件。

在单机阶段不提前实现这些组件。

## 15. 参考资料

- OIDC Discovery：<https://my.lsong.org/.well-known/openid-configuration>
- SQLite WAL：<https://www.sqlite.org/wal.html>
- SQLite 适用场景：<https://www.sqlite.org/whentouse.html>
- SQLite 实现限制：<https://www.sqlite.org/limits.html>
- PostgreSQL 事务隔离：<https://www.postgresql.org/docs/current/transaction-iso.html>
- Redis Pub/Sub（未来多机升级）：<https://redis.io/docs/latest/develop/pubsub/>
