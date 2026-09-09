-- Stop the old application before this migration.
-- Keep deleted grants, and reserve their keys only while they are live.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
CREATE UNIQUE INDEX IF NOT EXISTS stego_live_unique_a4b2f39ea673d046e72e82bd886a1565 ON role_bindings(gateway_id, role_id, user_id) WHERE deleted_at IS NULL;
DROP INDEX IF EXISTS composite_user_id_role_id_gateway_id;
COMMIT;
