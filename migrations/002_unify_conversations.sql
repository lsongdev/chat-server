DROP INDEX IF EXISTS conversations_direct_key_idx;
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS conversations_kind_check;
ALTER TABLE conversations DROP COLUMN IF EXISTS direct_key;
ALTER TABLE conversations DROP COLUMN IF EXISTS kind;

CREATE INDEX IF NOT EXISTS users_email_lookup_idx
    ON users ((lower(email))) WHERE email IS NOT NULL;
