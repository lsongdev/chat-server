-- Email is the required cross-client chat identity. All writers normalize it to
-- lowercase. Existing missing/duplicate emails must be reconciled before applying.
ALTER TABLE users ALTER COLUMN email SET NOT NULL;
CREATE UNIQUE INDEX users_email_identity_idx ON users ((lower(email)));
