# Flame Chat

一个单机优先的轻量聊天服务：Go 后端 + React 前端、PostgreSQL 和统一 Delivery WebSocket。客户端通过 OIDC 安全登录；Delivery 内核负责可靠投递，Messaging 中层负责 Room/Member/Capability，Chat 层负责产品业务。

产品中只有“会话”这一种交流容器。会话管理员使用完整邮件地址精确查找并添加已经登录过 Chat 的用户。

## 项目结构（前后端分离）

```
chat-server/
├── delivery/                                     # 最小投递内核：Event/Cursor/Connection/Bus
├── messaging/                                    # 通用消息框架：Room/Member/Capability
├── main.go handlers.go auth.go store.go          # Chat 业务、REST /api、鉴权与 PG adapter
└── frontend/                                      # 独立 React + Vite 前端
    ├── vite.config.ts                             # 8080 入口与后端代理
    ├── src/App.tsx                                # 路由与登录门控
    ├── src/useChatClient.ts                       # 会话/消息状态机 + WebSocket 同步
    └── src/pages/ components/                     # 登录 / 聊天 / 邀请 / 联系人 / 设置
```

Go 不嵌入或托管前端文件。开发时 Vite 监听 8080，提供 SPA fallback，并把 `/api`、`/auth`、`/realtime`、`/healthz` 和 `/readyz` 代理到监听 8081 的 Go 服务。Cloudflare Tunnel 指向 8080，浏览器和 iOS 始终只使用 `https://chat.lsong.org`。

Web 路由为 `/chat`（会话列表）、`/chat/{conversationID}`（聊天）、`/contacts` 和 `/settings`。所有客户端只保留服务端返回的活跃会话；离开、被移除或会话删除都会收敛为列表中不存在。会话名称在创建和重命名时均为必填。

## 登录方式

- iOS 和 Web 都访问 `GET /auth/login`，完成 OIDC Authorization Code + PKCE 后使用服务端签发的 HttpOnly Cookie。
- 身份提供方必须返回经过验证的邮箱；Chat API 不接收或保存提供方 access token。

用户头像使用 Gravatar：服务端对规范化邮件地址计算 SHA-256，浏览器直接加载头像；未配置头像时显示稳定的 identicon。

完整设计见 [docs/docs.md](docs/docs.md)，逐字段通信约定和消息报文见 [docs/protocol.md](docs/protocol.md)。

## 可选：准备 OIDC Client

在 `https://my.lsong.org` 创建 confidential client：

- grant type：`authorization_code`
- redirect URI：`https://chat.lsong.org/auth/callback`
- scopes：`openid profile email`
- PKCE：`S256`

复制环境变量模板并填写 client 凭据：

```sh
cp .env.example .env
```

同时把 `POSTGRES_PASSWORD` 换成长随机的 URL-safe 密码（推荐只使用字母、数字、`-` 和 `_`）。本地运行时如果使用其他字符，写入 `DATABASE_URL` 前必须进行 URL 编码。

## 启动

```sh
docker compose up -d  # 只启动 PostgreSQL
```

然后分别运行后端和前端：

```sh
source .env
go run .
```

```sh
source .env
npm ci --prefix frontend
npm run dev --prefix frontend
```

Vite 监听 8080，Go 监听 8081，PostgreSQL 映射到 `127.0.0.1:5432`。Cloudflare Tunnel 继续指向 8080；浏览器和 iOS 使用 `https://chat.lsong.org`。要求 Go 1.25+ 与 Node 20+。

## 验证

```sh
go test ./...
go vet ./...
cd frontend && npm run build && npm run lint
```

存储集成测试需要一个专用测试数据库：

```sh
CHAT_TEST_DATABASE_URL='postgres://chat:chat@localhost:5432/chat_test?sslmode=disable' go test ./...
```

不要把生产数据库传给 `CHAT_TEST_DATABASE_URL`。
