-- A shared CNPG database cannot use a deployment-cluster scope.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_databases'::regclass AND conname='hypershell_database_cluster_provider') THEN
  ALTER TABLE managed_databases ADD CONSTRAINT hypershell_database_cluster_provider CHECK (cluster_id IS NULL OR provider='deployment');
 END IF;
END $$;
COMMIT;
