-- Apply before the API starts. Existing placement requires separate verification.
-- Do not infer database location from a Gateway that could have moved.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS cluster_id text;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_databases'::regclass AND conname='fk_managed_databases_cluster_id_ref') THEN
  ALTER TABLE managed_databases ADD CONSTRAINT fk_managed_databases_cluster_id_ref FOREIGN KEY (cluster_id) REFERENCES managed_clusters(id);
 END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_managed_databases_cluster_id ON managed_databases (cluster_id);
COMMIT;
