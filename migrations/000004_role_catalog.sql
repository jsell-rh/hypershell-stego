-- Apply this migration before the role catalog API starts.
-- Existing role IDs and grants remain valid. Run again to repair seed metadata.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
ALTER TABLE roles ADD COLUMN IF NOT EXISTS display_name varchar(255);
ALTER TABLE roles ADD COLUMN IF NOT EXISTS description varchar(1024);
ALTER TABLE roles ADD COLUMN IF NOT EXISTS permissions jsonb;
ALTER TABLE roles ADD COLUMN IF NOT EXISTS built_in boolean NOT NULL DEFAULT false;
ALTER TABLE roles ALTER COLUMN built_in SET DEFAULT false;
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM roles WHERE name IN ('platform:admin','gateway:creator','gateway:owner','gateway:viewer') AND deleted_at IS NOT NULL) THEN
  RAISE EXCEPTION 'A built-in role is deleted. Restore it through a reviewed migration first.';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='roles'::regclass AND conname='roles_permissions_object') THEN
  ALTER TABLE roles ADD CONSTRAINT roles_permissions_object CHECK (permissions IS NULL OR jsonb_typeof(permissions)='object');
 END IF;
END $$;
INSERT INTO roles(id,name,display_name,description,permissions,built_in,created_time,updated_time) VALUES
 ('3J4dFWwyXQw80wNZfUyA4BCFBzY','platform:admin','Platform Administrator','Can read and delete all Gateways. Can manage placement records.','{"gateways":["read","delete"],"managed_clusters":["create","read","update","delete","list"],"gateway_releases":["create","read","update","delete","list"],"managed_databases":["create","read","update","delete","list"]}',true,now(),now()),
 ('3J4dFTnQjvBXpaSRP28ugn8ATpb','gateway:creator','Gateway Creator','Can create Gateways and read placement records. Receives an owner grant on creation.','{"gateways":["create"],"role_bindings":["create","read","delete","list"],"managed_clusters":["read","list"],"gateway_releases":["read","list"],"managed_databases":["read","list"]}',true,now(),now()),
 ('3J4dFUK9EDmspOqyUe20rDD4iM8','gateway:owner','Gateway Owner','Can read, change, and delete one Gateway. Can give owner and viewer access.','{"gateways":["read","update","delete"],"role_bindings":["create","read","delete","list"]}',true,now(),now()),
 ('3J4dFSkxpG300P1x43mVwHwL4AB','gateway:viewer','Gateway Viewer','Can read one Gateway.','{"gateways":["read"]}',true,now(),now())
ON CONFLICT(name) DO UPDATE SET
 display_name=EXCLUDED.display_name,
 description=EXCLUDED.description,
 permissions=EXCLUDED.permissions,
 built_in=true,
 updated_time=now()
WHERE ROW(roles.display_name,roles.description,roles.permissions,roles.built_in)
 IS DISTINCT FROM ROW(EXCLUDED.display_name,EXCLUDED.description,EXCLUDED.permissions,true);
COMMIT;
