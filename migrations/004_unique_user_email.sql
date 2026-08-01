-- Email is the required cross-client chat identity. Preserve legacy accounts that
-- predate this requirement with deterministic, non-routable identities so an
-- existing database can migrate without losing users or conversation history.
UPDATE users
SET email = 'legacy-' || id::text || '@users.invalid',
    email_verified = false
WHERE email IS NULL OR btrim(email) = '';

UPDATE users
SET email = lower(btrim(email));

-- Older releases allowed the same email on multiple OIDC subjects. Keep the
-- oldest account address and isolate the remaining records until they log in and
-- can be reconciled explicitly.
WITH ranked AS (
    SELECT id, row_number() OVER (
        PARTITION BY lower(email)
        ORDER BY created_at, id
    ) AS position
    FROM users
)
UPDATE users AS target
SET email = 'legacy-' || target.id::text || '@users.invalid',
    email_verified = false
FROM ranked
WHERE target.id = ranked.id AND ranked.position > 1;

ALTER TABLE users ALTER COLUMN email SET NOT NULL;
CREATE UNIQUE INDEX users_email_identity_idx ON users ((lower(email)));
