# Flame Client–Server Protocol

This document is the wire contract between `chat-server`, Flame iOS, and the browser client. JSON keys use `snake_case`, UUIDs are lowercase strings, timestamps use RFC 3339, and email comparisons are case-insensitive after trimming and lowercasing.

## Identity and session

Email is the unique chat identity across every client. Internal UUIDs are database references and may appear in packets, but clients must not use them to decide whether two people are the same user. OIDC `sub` identifies an authorization-provider account only; after OIDC callback its email resolves the chat user.

Native login:

```http
POST /auth/email
Content-Type: application/json
Origin: https://chat.example.com

{"name":"Alice","email":"alice@example.com"}
```

The response is the user object below and a `chat_session` or `__Host-chat_session` HttpOnly cookie. The native flow does not prove ownership of the email; public deployments should add email verification before exposing it.

```json
{
  "id": "2ca91885-44af-4fac-a0ef-5ca45fd0e28e",
  "display_name": "Alice",
  "email": "alice@example.com",
  "email_verified": true,
  "avatar_url": "https://gravatar.com/avatar/...",
  "status": "active"
}
```

Browser login starts at `GET /auth/login`. The server uses Authorization Code, PKCE, state, and nonce, reads `name` and `email` from the verified OIDC result, resolves the same email identity, then issues the same chat session cookie. Clients never send the provider access token to chat APIs.

All `/api/*` and `/ws` requests use this cookie. Mutations send an allowed `Origin`. `POST /auth/logout` deletes the server session and expires the cookie.

## HTTP resources

Successful list responses are wrapped (`{"conversations":[]}`, `{"members":[]}`, `{"contacts":[]}`, `{"events":[]}`). Create/update responses return the created resource or an event. Successful deletions return `204`.

Errors have one shape:

```json
{"error":{"code":"forbidden","message":"你没有执行此操作的权限"}}
```

### Conversations

Create:

```http
POST /api/conversations
{"title":"Project Flame"}
```

```json
{
  "id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425",
  "title":"Project Flame",
  "last_seq":1,
  "last_read_seq":1,
  "joined_seq":1,
  "role":"owner",
  "status":"active",
  "updated_at":"2026-08-01T12:00:00Z"
}
```

- `GET /api/conversations` returns every active or previously-left conversation visible to the current user.
- `PATCH /api/conversations/{id}` with `{"title":"New title"}` requires owner/admin.
- `DELETE /api/conversations/{id}` permanently deletes the conversation for every member and requires owner.
- `POST /api/conversations/{id}/leave` leaves it only for the current member and transfers ownership when necessary.

### Members

- `GET /api/conversations/{id}/members` lists current and historical members.
- `POST /api/conversations/{id}/members` with `{"email":"bob@example.com"}` adds the unique user matching that email.
- `PATCH /api/conversations/{id}/members/{userID}` with `{"role":"admin"}` or `{"role":"member"}` is owner-only.
- `DELETE /api/conversations/{id}/members/{userID}` removes a member. An admin can remove members; only an owner can manage admins.

Member packet:

```json
{
  "user_id":"b74f01c7-f243-4298-9202-e92db5e0f3f6",
  "display_name":"Bob",
  "email":"bob@example.com",
  "email_verified":true,
  "avatar_url":"https://gravatar.com/avatar/...",
  "role":"member",
  "status":"active",
  "joined_seq":2
}
```

### Contacts

Contacts belong to the signed-in user and are separate from conversation membership.

- `GET /api/contacts`
- `POST /api/contacts` with `{"name":"Bob","email":"bob@example.com","note":"Design"}`
- `PUT /api/contacts/{contactID}` with the same fields
- `DELETE /api/contacts/{contactID}`

The returned contact may contain `linked_user_id` when that email has already logged into Chat. Adding a contact does not add them to a conversation.

## Event stream and message packet

Every conversation owns a strictly increasing `seq`. Messages and metadata changes share the sequence, so `last_seq` is not a message count. An event is identified by `(conversation_id, seq)`; `id` is its globally unique event UUID.

```json
{
  "conversation_id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425",
  "seq":7,
  "id":"6426f043-14f0-4771-8522-acc9c4070908",
  "sender_id":"2ca91885-44af-4fac-a0ef-5ca45fd0e28e",
  "sender_email":"alice@example.com",
  "sender_name":"Alice",
  "type":"message.created",
  "payload":{"text":"hello"},
  "created_at":"2026-08-01T12:01:02.345Z"
}
```

Supported event types and payloads:

| Event | Payload |
| --- | --- |
| `conversation.created` | `{"title":"...","created_by":"user-uuid"}` |
| `conversation.renamed` | `{"title":"..."}` |
| `message.created` | `{"text":"..."}` |
| `member.joined` | `{"user_id":"...","email":"...","role":"member"}` |
| `member.left` | `{"user_id":"...","new_owner_id":"... or null"}` |
| `member.removed` | `{"user_id":"...","removed_by":"..."}` |
| `member.role_changed` | `{"user_id":"...","role":"admin or member"}` |

Fetch history with `GET /api/conversations/{id}/events?after_seq=6&limit=200`. Results are ascending and include only the membership period visible to that user. Mark a contiguous cursor read with `POST /api/conversations/{id}/read` and `{"seq":7}`.

HTTP message sending uses an idempotency UUID:

```http
POST /api/conversations/{id}/messages

{"client_message_id":"99780747-d253-494f-81ec-19e380defdd1","text":"hello"}
```

Retrying the same `client_message_id` for the same sender and conversation returns the original stored event.

## WebSocket protocol version 1

Connect to `wss://host/ws` with the session cookie and allowed `Origin`. The server first sends:

```json
{"type":"hello","protocol_version":1,"user_id":"2ca91885-44af-4fac-a0ef-5ca45fd0e28e"}
```

Send a message:

```json
{
  "type":"message.send",
  "request_id":"ui-operation-uuid",
  "conversation_id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425",
  "client_message_id":"99780747-d253-494f-81ec-19e380defdd1",
  "content":{"text":"hello"}
}
```

The sender first receives durable acknowledgement:

```json
{"type":"message.stored","request_id":"ui-operation-uuid","conversation_id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425","seq":7,"message_id":"6426f043-14f0-4771-8522-acc9c4070908"}
```

Every online member then receives `{"type":"conversation.event","event":{...}}`. A `conversation.deleted` notification contains `conversation_id` and has no sequence because the stream has been removed.

Read update and acknowledgement:

```json
{"type":"read.update","request_id":"uuid","conversation_id":"conversation-uuid","seq":7}
{"type":"read.updated","request_id":"uuid","conversation_id":"conversation-uuid","seq":7}
```

Protocol failures are `{"type":"error","request_id":"uuid","code":"invalid_request"}`. Known codes are `unknown_event_type`, `invalid_request`, `invalid_message`, `rate_limited`, `forbidden`, `store_failed`, and `invalid_sequence`.

## Sync algorithm

WebSocket is a low-latency hint, while PostgreSQL and HTTP event history are authoritative:

1. Persist local outgoing content with a stable `client_message_id`.
2. Connect WebSocket and fetch the conversation list.
3. For each conversation, compare local contiguous cursor with `last_seq`.
4. Page `/events` after the local cursor; accept only `seq == cursor + 1` and then advance it.
5. Merge events by `(conversation_id, seq)` and messages by server event `id`.
6. On a WebSocket event or sequence gap, schedule another HTTP synchronization pass.
7. Retry failed sends with the same `client_message_id` and exponential backoff.

Unknown future event types must still advance the contiguous cursor after their envelope is stored or safely ignored; otherwise older clients would loop forever.
