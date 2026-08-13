# Flame Client–Server Protocol

This is the wire contract shared by `chat-server`, Flame iOS, and the browser client. JSON keys use `snake_case`, UUIDs are lowercase strings, timestamps use RFC 3339, and email comparison is case-insensitive after trimming and lowercasing.

## Session

Both clients use the same MyCenter OIDC identity. Browsers start at `GET /auth/login`; the callback creates an opaque HttpOnly `chat_session` cookie and redirects to the validated `return_to` path. iOS starts at `GET /auth/mobile/login` with an app PKCE challenge, receives a two-minute single-use code at the fixed `flame://auth/callback`, and exchanges it with the verifier at `POST /auth/mobile/token` for that same session cookie. The upstream OIDC exchange always uses Authorization Code with PKCE, state, and nonce, and requires a verified email. Clients never receive provider tokens.

All `/api/*` and `/realtime` requests use that cookie. Mutations and the WebSocket handshake include an allowed `Origin`. `POST /auth/logout` revokes the server session and expires the cookie. `GET /api/me` returns the signed-in user.

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

Business metadata continues to use HTTP. Messages are published through the authenticated Delivery WebSocket with a stable client UUID and content structured exactly once:

```json
{
  "op":"publish",
  "id":"99780747-d253-494f-81ec-19e380defdd1",
  "room_id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425",
  "name":"message.created",
  "profile":"durable",
  "data":{"type":"text","text":"hello"}
}
```

Non-text content uses `{"type":"image","data":{...}}`, with the same shape for audio, location, files, and contacts. Retrying the same publish `id` for the same sender and Room returns the original event. Clients merge their outbox using the event `publish_id`, then store the server event `id` and `sequence`.

## Delivery WebSocket protocol v1

Connect to `wss://host/realtime` with WebSocket subprotocol `delivery.v1`, the session cookie, and an allowed `Origin`. Packets are UTF-8 JSON; text frames are canonical, while binary frames containing UTF-8 JSON are accepted for native-client compatibility. The first packet is:

```json
{
  "op":"hello",
  "protocol":"delivery.v1",
  "connection_id":"019ff9d1-...",
  "identity_id":"2ca91885-44af-4fac-a0ef-5ca45fd0e28e",
  "max_message_bytes":65536
}
```

The server derives Room routing from active conversation membership; clients do not subscribe to arbitrary topics. After hello, a client supplies the last contiguous cursor for each locally known Room:

```json
{"op":"resume","rooms":{"6d2718e6-39ee-4144-ab8c-aaaf21c1a425":6}}
```

Durable publish succeeds only after PostgreSQL commits:

```json
{
  "op":"ack",
  "id":"99780747-d253-494f-81ec-19e380defdd1",
  "status":"committed",
  "event_id":"6426f043-14f0-4771-8522-acc9c4070908",
  "sequence":7
}
```

Every online Room member, including the sender, receives the resulting fact:

```json
{
  "op":"event",
  "room_id":"6d2718e6-39ee-4144-ab8c-aaaf21c1a425",
  "id":"6426f043-14f0-4771-8522-acc9c4070908",
  "publish_id":"99780747-d253-494f-81ec-19e380defdd1",
  "name":"message.created",
  "profile":"durable",
  "sequence":7,
  "actor_id":"2ca91885-44af-4fac-a0ef-5ca45fd0e28e",
  "data":{"type":"text","text":"hello"},
  "created_at":"2026-08-01T12:01:02.345Z"
}
```

`ephemeral` uses the same publish/event envelope but returns `status:"accepted"`, has no durable sequence, and is not recovered. Flame accepts it only as `rtc.signal` with a `webrtc:*` payload type; the server assigns a 30-second TTL, and expired or backpressured signals may be dropped. Chat clients cannot publish metadata facts such as rename or membership events. `stream` is reserved in the envelope but is not accepted until snapshot and resume semantics are implemented.

The server sends `room.added` and `room.removed` when membership routing changes. These are realtime control messages; the active conversation HTTP list remains authoritative.

## Sync algorithm

1. Persist outgoing durable content in a local outbox with stable Room and publish UUIDs.
2. Fetch `/api/me` once after login, connect `/realtime`, and fetch active conversations.
3. Remove locally synced conversations absent from the active server list.
4. Send `resume` cursors and accept durable events only when `sequence == cursor + 1`; HTTP history remains a fallback for foreground reconciliation.
5. Merge events by `(room_id, sequence)` and optimistic messages by `publish_id`.
6. On a gap, reconnect, foreground activation, or Room routing change, refresh business metadata and synchronize again.
7. Keep an unacknowledged publish in the SDK outbox and resend the same UUID after reconnection. UI observes delivery state but does not run its own retry timer.
