# Flame Chat

一个单机优先的轻量聊天服务：Go 后端 + React 前端、PostgreSQL 和 WebSocket。所有客户端只需姓名和邮箱即可建立本站会话；Web 也可以选择通过 OIDC 获取邮箱。两种方式都会按规范化邮箱归并到同一个聊天用户。

产品中只有“会话”这一种交流容器。会话管理员使用完整邮件地址精确查找并添加已经登录过 Chat 的用户。

## 项目结构（前后端分离）

```
chat-server/
├── main.go handlers.go auth.go hub.go store.go   # Go 后端：REST /api + WebSocket /ws + 鉴权
├── web.go                                         # 托管构建后的 React 静态文件（SPA fallback）
└── frontend/                                      # React + Vite 前端
    ├── src/App.tsx                                # 路由与登录门控
    ├── src/useChatClient.ts                       # 会话/消息状态机 + WebSocket 同步
    └── src/pages/ components/                     # 登录 / 聊天 / 邀请 / 联系人 / 设置
```

前端只通过 `fetch /api/*` 和 `WebSocket /ws` 与后端通信，Cookie 由后端 HttpOnly 设置，生产环境同源部署、由 Go 直接托管。

Web 路由为 `/chat`（会话列表）、`/chat/{conversationID}`（聊天）、`/contacts` 和 `/settings`。Web 只展示 `status=active` 的会话；离开会话的历史可继续由原生客户端按协议处理。会话名称在创建和重命名时均为必填。

## 登录方式

- iOS 和 Web：`POST /auth/email`，JSON 只包含 `name` 和 `email`，成功后使用服务端设置的 HttpOnly Cookie。
- 可选 MyCenter 登录：访问 `GET /auth/login`，完成 OIDC Authorization Code + PKCE 后由服务端读取姓名和邮箱，并设置同一种 Cookie。

简易邮箱登录不验证邮箱所有权，适合受信部署或早期产品。公开部署如需防止冒用，应在这个接口前增加邮件验证码或组织网关。

用户头像使用 Gravatar：服务端对规范化邮件地址计算 SHA-256，浏览器直接加载头像；未配置头像时显示稳定的 identicon。

完整设计见 [docs/docs.md](docs/docs.md)，逐字段通信约定和消息报文见 [docs/protocol.md](docs/protocol.md)。

## 可选：准备 OIDC Client

在 `https://my.lsong.org` 创建 confidential client：

- grant type：`authorization_code`
- redirect URI：`http://localhost:8080/auth/callback`（本地）
- scopes：`openid profile email`
- PKCE：`S256`

复制环境变量模板并填写 client 凭据：

```sh
cp .env.example .env
```

同时把 `POSTGRES_PASSWORD` 换成长随机的 URL-safe 密码（推荐只使用字母、数字、`-` 和 `_`）。本地运行时如果使用其他字符，写入 `DATABASE_URL` 前必须进行 URL 编码。

## 前端

```sh
cd frontend
npm install
npm run dev      # 开发服务器，Vite 将 /api、/auth、/ws 代理到 Go 后端
npm run build    # 产物输出到 frontend/dist，由 Go 服务托管
npm run lint
```

开发时前端跑在 `http://localhost:5173`，需要把 `ALLOWED_ORIGINS` 中加入该地址（`.env.example` 已默认包含）。

## Docker Compose 启动

```sh
docker compose up --build
```

打开 <http://localhost:8080>。镜像内先构建前端再构建 Go 二进制，应用启动时会在 PostgreSQL 中执行尚未应用的版本化 migration。

## 本地启动

### 方式一：Go 托管（生产形态）

先构建前端，再直接跑 Go：

```sh
cd frontend && npm ci && npm run build && cd ..
set -a
source .env
set +a
go run .
```

打开 <http://localhost:8080>。

### 方式二：前后端分离开发

后端：

```sh
set -a
source .env
set +a
go run .
```

前端（Vite dev server + 代理）：

```sh
cd frontend
npm run dev
```

打开 <http://localhost:5173>。要求 Go 1.25+ 与 Node 20+。

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
