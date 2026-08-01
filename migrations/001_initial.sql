CREATE TABLE IF NOT EXISTS schema_migrations (
    version text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    oidc_issuer text NOT NULL,
    oidc_subject text NOT NULL,
    username text,
    display_name text,
    email text,
    email_verified boolean NOT NULL DEFAULT false,
    picture_url text,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    UNIQUE (oidc_issuer, oidc_subject)
);

CREATE TABLE auth_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    user_agent text,
    ip inet
);
CREATE INDEX auth_sessions_expiry_idx ON auth_sessions (expires_at);

CREATE TABLE oidc_login_attempts (
    state_hash bytea PRIMARY KEY,
    nonce text NOT NULL,
    code_verifier text NOT NULL,
    return_to text NOT NULL DEFAULT '/',
    expires_at timestamptz NOT NULL
);
CREATE INDEX oidc_login_attempts_expiry_idx ON oidc_login_attempts (expires_at);

CREATE TABLE conversations (
    id uuid PRIMARY KEY,
    title text,
    created_by uuid NOT NULL REFERENCES users(id),
    last_seq bigint NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE conversation_members (
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
CREATE INDEX conversation_members_user_idx
    ON conversation_members (user_id, conversation_id);

CREATE TABLE conversation_member_periods (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_seq bigint NOT NULL,
    left_seq bigint,
    joined_at timestamptz NOT NULL DEFAULT now(),
    left_at timestamptz,
    leave_reason text CHECK (leave_reason IN ('left', 'removed'))
);
CREATE INDEX conversation_member_periods_lookup_idx
    ON conversation_member_periods (conversation_id, user_id, joined_seq);

CREATE TABLE conversation_events (
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
CREATE UNIQUE INDEX conversation_events_idempotency_idx
    ON conversation_events (conversation_id, sender_id, client_event_id)
    WHERE client_event_id IS NOT NULL;
CREATE INDEX conversation_events_sender_idx
    ON conversation_events (sender_id, created_at DESC);

CREATE TABLE conversation_invites (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    created_by uuid NOT NULL REFERENCES users(id),
    max_uses integer NOT NULL DEFAULT 1 CHECK (max_uses > 0),
    use_count integer NOT NULL DEFAULT 0 CHECK (use_count >= 0),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX conversation_invites_expiry_idx ON conversation_invites (expires_at);
