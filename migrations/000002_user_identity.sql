-- Stop the old API before this migration. It uses username as identity.
-- Preserve legacy users and grants. Do not infer identity from a username.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
ALTER TABLE users ADD COLUMN IF NOT EXISTS issuer varchar(1024);
ALTER TABLE users ADD COLUMN IF NOT EXISTS subject varchar(255);
CREATE UNIQUE INDEX IF NOT EXISTS composite_issuer_subject ON users (issuer, subject);
DROP INDEX IF EXISTS idx_users_username;
COMMIT;
