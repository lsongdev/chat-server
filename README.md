# Flame Chat

一个单机优先的轻量聊天服务：Go、PostgreSQL、OIDC 和 WebSocket。消息与成员变化使用同一条会话事件序列，在线事件由进程内 Hub 分发，离线消息通过序号补齐。

完整设计见 [docs/docs.md](docs/docs.md)。

## 准备 OIDC Client

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
