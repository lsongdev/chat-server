# Flame Chat Web

独立的 React + TypeScript + Vite 前端。它不依赖 Go 源码或后端构建产物。

## 开发

```sh
npm ci
npm run dev
```

Vite 默认把 `/api`、`/auth`、`/realtime`、`/healthz` 和 `/readyz` 代理到 `http://127.0.0.1:8081`。可通过 `CHAT_BACKEND` 指向其他后端：

```sh
CHAT_BACKEND=http://127.0.0.1:9000 npm run dev
```

## 构建与镜像

```sh
npm run build
npm run lint
docker build -t flame-chat-frontend .
```

生产镜像使用 Nginx 提供 SPA，并把协议端点反向代理到 Compose 网络中的 `backend:8080`。前端和后端分别构建、分别运行，但对浏览器保持同源。

## Client routes

- `/chat` — 会话列表；移动端只显示列表。
- `/chat/{conversationID}` — 指定会话；移动端只显示聊天页并提供返回按钮。
- `/contacts` — 联系人。
- `/settings` — 应用设置。

Web 会过滤服务端返回的 `status: "left"` 会话；会话名称是必填字段。
