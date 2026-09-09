-- Apply before the API that projects global roles starts.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
ALTER TABLE role_bindings ALTER COLUMN gateway_id DROP NOT NULL;
ALTER TABLE role_bindings DROP CONSTRAINT IF EXISTS chk_role_bindings_scope;
ALTER TABLE role_bindings ADD CONSTRAINT chk_role_bindings_scope CHECK (
 (scope='gateway' AND gateway_id IS NOT NULL) OR
 (scope='global' AND gateway_id IS NULL)
);
-- Gateway grants retain their existing live composite key. Global grants have
-- no Gateway, so their live key contains only the role and user.
CREATE UNIQUE INDEX IF NOT EXISTS role_bindings_live_global_key
 ON role_bindings(role_id,user_id) WHERE deleted_at IS NULL AND scope='global';
COMMIT;
