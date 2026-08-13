-- Final core schema.

CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY,
    oidc_issuer text NOT NULL,
    oidc_subject text NOT NULL,
    username text,
    display_name text,
    email text NOT NULL,
    email_verified boolean NOT NULL DEFAULT false,
    picture_url text,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    UNIQUE (oidc_issuer, oidc_subject)
);

CREATE UNIQUE INDEX IF NOT EXISTS users_email_identity_idx ON users ((lower(email)));

CREATE TABLE IF NOT EXISTS auth_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    user_agent text,
    ip inet
);
CREATE INDEX IF NOT EXISTS auth_sessions_expiry_idx ON auth_sessions (expires_at);

CREATE TABLE IF NOT EXISTS oidc_login_attempts (
    state_hash bytea PRIMARY KEY,
    nonce text NOT NULL,
    code_verifier text NOT NULL,
    return_to text NOT NULL DEFAULT '/',
    mobile_challenge text,
    expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS oidc_login_attempts_expiry_idx ON oidc_login_attempts (expires_at);

CREATE TABLE IF NOT EXISTS mobile_login_codes (
    code_hash bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_challenge text NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS mobile_login_codes_expiry_idx ON mobile_login_codes (expires_at);

CREATE TABLE IF NOT EXISTS conversations (
    id uuid PRIMARY KEY,
    title text,
    created_by uuid NOT NULL REFERENCES users(id),
    last_seq bigint NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS conversation_members (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member')),
    joined_seq bigint NOT NULL CHECK (joined_seq >= 0),
    left_seq bigint,
    last_read_seq bigint NOT NULL DEFAULT 0 CHECK (last_read_seq >= 0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'left', 'removed')),
    joined_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX IF NOT EXISTS conversation_members_user_idx
    ON conversation_members (user_id, conversation_id);

CREATE TABLE IF NOT EXISTS conversation_events (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    seq bigint NOT NULL,
    id uuid NOT NULL UNIQUE,
    sender_id uuid REFERENCES users(id),
    client_event_id uuid,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, seq)
);
CREATE UNIQUE INDEX IF NOT EXISTS conversation_events_idempotency_idx
    ON conversation_events (conversation_id, sender_id, client_event_id)
    WHERE client_event_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS conversation_events_sender_idx
    ON conversation_events (sender_id, created_at DESC);

CREATE TABLE IF NOT EXISTS contacts (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    email text NOT NULL,
    note text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, email)
);
CREATE INDEX IF NOT EXISTS contacts_owner_idx ON contacts (owner_id, lower(name), lower(email));

DROP TABLE IF EXISTS conversation_member_periods;
DROP TABLE IF EXISTS conversation_invites;
