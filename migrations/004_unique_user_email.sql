-- Email is the cross-client chat identity. All writers normalize it to lowercase.
-- Existing duplicate data must be reconciled before applying this migration.
CREATE UNIQUE INDEX users_email_identity_idx
    ON users ((lower(email))) WHERE email IS NOT NULL;
