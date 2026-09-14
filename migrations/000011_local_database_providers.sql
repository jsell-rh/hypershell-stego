-- New databases require explicit local placement. Preserve old rows and data.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
ALTER TABLE managed_databases DROP CONSTRAINT IF EXISTS hypershell_database_cluster_provider;
ALTER TABLE managed_databases DROP CONSTRAINT IF EXISTS chk_managed_databases_provider;
ALTER TABLE managed_databases ADD CONSTRAINT chk_managed_databases_provider
 CHECK (provider IN ('cnpg', 'external')) NOT VALID;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_databases'::regclass AND conname='hypershell_database_locality') THEN
  ALTER TABLE managed_databases ADD CONSTRAINT hypershell_database_locality
   CHECK (cluster_id IS NOT NULL) NOT VALID;
 END IF;
END $$;
COMMIT;
