# Flame Chat

一个单机优先的轻量聊天服务：Go、PostgreSQL 和 WebSocket。所有客户端只需姓名和邮箱即可建立本站会话；Web 也可以选择通过 OIDC 获取邮箱。两种方式都会按规范化邮箱归并到同一个聊天用户。

产品中只有“会话”这一种交流容器。会话管理员使用完整邮件地址精确查找并添加已经登录过 Chat 的用户。

核心接口包括会话创建/删除/退出、会话重命名、成员增删与管理员角色、个人通讯录 CRUD、事件同步和消息发送。`DELETE /api/conversations/{id}` 仅允许所有者删除整个会话；普通成员使用 `POST /api/conversations/{id}/leave` 退出。

## 登录方式

- iOS 和 Web：`POST /auth/email`，JSON 只包含 `name` 和 `email`，成功后使用服务端设置的 HttpOnly Cookie。Web 根页面在未登录时显示该表单。
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

## Docker Compose 启动

```sh
docker compose up --build
```

打开 <http://localhost:8080>。应用启动时会在 PostgreSQL 中执行尚未应用的版本化 migration。

## 本地 Go 启动

先启动 PostgreSQL，把 `.env` 中的 `DATABASE_URL` 保持为 `127.0.0.1` 地址，然后导出变量：

```sh
set -a
source .env
set +a
go run .
```

要求 Go 1.25 或更高版本。

## 验证

不依赖外部服务的测试：

```sh
go test ./...
go vet ./...
```

存储集成测试需要一个专用测试数据库：

```sh
CHAT_TEST_DATABASE_URL='postgres://chat:chat@localhost:5432/chat_test?sslmode=disable' go test ./...
```

不要把生产数据库传给 `CHAT_TEST_DATABASE_URL`。
