# Flame Client–Server Protocol

This is the wire contract shared by `chat-server`, Flame iOS, and the browser client. JSON keys use `snake_case`, UUIDs are lowercase strings, timestamps use RFC 3339, and email comparison is case-insensitive after trimming and lowercasing.

## Session

Both clients use the same MyCenter OIDC identity. Browsers start at `GET /auth/login`; the callback creates an opaque HttpOnly `chat_session` cookie and redirects to the validated `return_to` path. iOS starts at `GET /auth/mobile/login` with an app PKCE challenge, receives a two-minute single-use code at the fixed `flame://auth/callback`, and exchanges it with the verifier at `POST /auth/mobile/token` for that same session cookie. The upstream OIDC exchange always uses Authorization Code with PKCE, state, and nonce, and requires a verified email. Clients never receive provider tokens.

All `/api/*` and `/ws` requests use that cookie. Mutations include an allowed `Origin`. `POST /auth/logout` revokes the server session and expires the cookie. `GET /api/me` returns the signed-in user.

Chat API errors always have one shape:

```json
{"error":{"code":"forbidden","message":"你没有执行此操作的权限"}}
```

## Conversations

Only active conversations are returned by `GET /api/conversations`. Leaving, removal, and deletion therefore converge by absence from this list. A conversation contains a durable event cursor and a message-only unread count:

```json
{
  "id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425",
  "title":"Project Flame",
  "last_seq":7,
  "last_read_seq":5,
  "unread_count":1,
  "joined_seq":1,
  "role":"owner",
  "status":"active",
  "updated_at":"2026-08-01T12:00:00Z"
}
```

Creation is idempotent because the client supplies the conversation UUID:

```http
POST /api/conversations

{"id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425","title":"Project Flame"}
```

- `PATCH /api/conversations/{id}` renames a conversation.
- `POST /api/conversations/{id}/leave` leaves it for the current user.
- `DELETE /api/conversations/{id}` permanently deletes it for every member and is owner-only.
- `GET/POST /api/conversations/{id}/members` lists or adds members.
- `PATCH/DELETE /api/conversations/{id}/members/{userID}` changes a role or removes a member.

## Contacts

Contacts are server-owned and synchronized by every client:

- `GET /api/contacts`
- `POST /api/contacts`
- `PUT /api/contacts/{contactID}`
- `DELETE /api/contacts/{contactID}`

Using `PUT` with a client-generated UUID makes native saves naturally retryable. A contact may contain `linked_user_id` when its email belongs to an existing user. Contacts and conversation membership are separate concepts.

## Durable events and messages

Every conversation has one strictly increasing `seq`. Messages and metadata changes share it, so `last_seq` is a cursor, not a message count. Events are identified by `(conversation_id, seq)`.

```json
{
  "conversation_id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425",
  "seq":7,
  "id":"6426f043-14f0-4771-8522-acc9c4070908",
  "sender_id":"2ca91885-44af-4fac-a0ef-5ca45fd0e28e",
  "client_message_id":"99780747-d253-494f-81ec-19e380defdd1",
  "sender_email":"alice@example.com",
  "sender_name":"Alice",
  "type":"message.created",
  "payload":{"type":"text","text":"hello"},
  "created_at":"2026-08-01T12:01:02.345Z"
}
```

Fetch ascending history with `GET /api/conversations/{id}/events?after_seq=6&limit=200`. Unknown future event types still advance the contiguous cursor. Mark a cursor read with `POST /api/conversations/{id}/read` and `{"seq":7}`.

All writes use HTTP. Message content is structured once, without JSON encoded inside a string:

```http
POST /api/conversations/{id}/messages

{"client_message_id":"99780747-d253-494f-81ec-19e380defdd1","content":{"type":"text","text":"hello"}}
```

Non-text content uses `{"type":"image","data":{...}}`, with the same shape for audio, location, files, contacts, and signaling packets. Retrying the same `client_message_id` for the same sender and conversation returns the original event. Clients merge their outbox using `client_message_id`, then store the server event `id` and `seq`.

## WebSocket protocol version 2

WebSocket is a one-way invalidation channel. Connect to `wss://host/ws` with the session cookie and allowed `Origin`. The first packet is:

```json
{"type":"hello","protocol_version":2,"user_id":"2ca91885-44af-4fac-a0ef-5ca45fd0e28e"}
```

Changes produce a compact hint:

```json
{"type":"conversation.changed","conversation_id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425","last_seq":7}
```

Permanent deletion produces:

```json
{"type":"conversation.deleted","conversation_id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425"}
```

Clients do not send application packets over WebSocket. PostgreSQL and HTTP history remain authoritative.

## Sync algorithm

1. Persist outgoing content with stable client conversation and message UUIDs.
2. Fetch `/api/me` once after login, connect WebSocket, and fetch active conversations.
3. Remove locally synced conversations absent from the active server list.
4. Page `/events` after each local contiguous cursor; accept only `seq == cursor + 1`.
5. Merge events by `(conversation_id, seq)` and optimistic messages by `client_message_id`.
6. On a WebSocket hint, reconnect, foreground activation, or a sequence gap, run another HTTP synchronization pass.
7. Retry failed writes with the same client UUID and exponential backoff.
