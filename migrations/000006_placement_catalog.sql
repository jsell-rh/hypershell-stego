-- Stop the API. Supply the required data for existing catalog rows first.
-- This migration must not invent providers, images, or secret references.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
ALTER TABLE managed_clusters ALTER COLUMN name SET NOT NULL;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_clusters'::regclass AND conname='chk_managed_clusters_name') THEN
  ALTER TABLE managed_clusters ADD CONSTRAINT chk_managed_clusters_name CHECK (length(name) >= 1);
 END IF;
END $$;
ALTER TABLE managed_clusters ADD COLUMN IF NOT EXISTS provider varchar(64);
ALTER TABLE managed_clusters ALTER COLUMN provider SET NOT NULL;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_clusters'::regclass AND conname='chk_managed_clusters_provider') THEN
  ALTER TABLE managed_clusters ADD CONSTRAINT chk_managed_clusters_provider CHECK (length(provider) >= 1);
 END IF;
END $$;
ALTER TABLE managed_clusters ADD COLUMN IF NOT EXISTS region varchar(255);
ALTER TABLE managed_clusters ADD COLUMN IF NOT EXISTS kubeconfig_secret varchar(253);
ALTER TABLE managed_clusters ALTER COLUMN kubeconfig_secret SET NOT NULL;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_clusters'::regclass AND conname='chk_managed_clusters_kubeconfig_secret') THEN
  ALTER TABLE managed_clusters ADD CONSTRAINT chk_managed_clusters_kubeconfig_secret CHECK (length(kubeconfig_secret) >= 1);
 END IF;
END $$;
ALTER TABLE managed_clusters ADD COLUMN IF NOT EXISTS status varchar(255);
ALTER TABLE managed_clusters ADD COLUMN IF NOT EXISTS api_server_url varchar(2048);
ALTER TABLE gateway_releases ALTER COLUMN name SET NOT NULL;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='gateway_releases'::regclass AND conname='chk_gateway_releases_name') THEN
  ALTER TABLE gateway_releases ADD CONSTRAINT chk_gateway_releases_name CHECK (length(name) >= 1);
 END IF;
END $$;
ALTER TABLE gateway_releases ADD COLUMN IF NOT EXISTS image varchar(2048);
ALTER TABLE gateway_releases ALTER COLUMN image SET NOT NULL;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='gateway_releases'::regclass AND conname='chk_gateway_releases_image') THEN
  ALTER TABLE gateway_releases ADD CONSTRAINT chk_gateway_releases_image CHECK (length(image) >= 1);
 END IF;
END $$;
ALTER TABLE gateway_releases ADD COLUMN IF NOT EXISTS rollout_strategy varchar(64);
ALTER TABLE gateway_releases ADD COLUMN IF NOT EXISTS canary_percent integer;
ALTER TABLE gateway_releases ADD COLUMN IF NOT EXISTS canary_duration varchar(64);
ALTER TABLE gateway_releases ADD COLUMN IF NOT EXISTS status varchar(255);
ALTER TABLE managed_databases ALTER COLUMN name SET NOT NULL;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_databases'::regclass AND conname='chk_managed_databases_name') THEN
  ALTER TABLE managed_databases ADD CONSTRAINT chk_managed_databases_name CHECK (length(name) >= 1);
 END IF;
END $$;
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS provider text;
ALTER TABLE managed_databases ALTER COLUMN provider SET NOT NULL;
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS namespace varchar(29);
ALTER TABLE managed_databases ALTER COLUMN namespace SET NOT NULL;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_databases'::regclass AND conname='chk_managed_databases_namespace') THEN
  ALTER TABLE managed_databases ADD CONSTRAINT chk_managed_databases_namespace CHECK (length(namespace) >= 1);
 END IF;
END $$;
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS region varchar(255);
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS engine varchar(64);
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS engine_version varchar(64);
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS instance_class varchar(255);
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS connection_secret varchar(253);
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS status varchar(255);
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_databases'::regclass AND conname='chk_managed_databases_provider') THEN
  ALTER TABLE managed_databases ADD CONSTRAINT chk_managed_databases_provider CHECK (provider IN ('cnpg','deployment'));
 END IF;
END $$;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='gateway_releases'::regclass AND conname='chk_gateway_releases_canary_percent') THEN
  ALTER TABLE gateway_releases ADD CONSTRAINT chk_gateway_releases_canary_percent CHECK (canary_percent >= 0 AND canary_percent <= 100);
 END IF;
END $$;
CREATE UNIQUE INDEX IF NOT EXISTS idx_managed_databases_namespace ON managed_databases(namespace);
UPDATE roles SET permissions=permissions || '{"managed_clusters":["create","read","update","delete","list"],"gateway_releases":["create","read","update","delete","list"],"managed_databases":["create","read","update","delete","list"]}'::jsonb, description='Can read and delete all Gateways. Can manage placement records.', updated_time=now() WHERE name='platform:admin' AND (NOT permissions @> '{"managed_clusters":["create","read","update","delete","list"],"gateway_releases":["create","read","update","delete","list"],"managed_databases":["create","read","update","delete","list"]}'::jsonb OR description IS DISTINCT FROM 'Can read and delete all Gateways. Can manage placement records.');
UPDATE roles SET permissions=permissions || '{"managed_clusters":["read","list"],"gateway_releases":["read","list"],"managed_databases":["read","list"]}'::jsonb, description='Can create Gateways and read placement records. Receives an owner grant on creation.', updated_time=now() WHERE name='gateway:creator' AND (NOT permissions @> '{"managed_clusters":["read","list"],"gateway_releases":["read","list"],"managed_databases":["read","list"]}'::jsonb OR description IS DISTINCT FROM 'Can create Gateways and read placement records. Receives an owner grant on creation.');
COMMIT;
