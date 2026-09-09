BEGIN;
ALTER TABLE "managed_databases" ADD COLUMN IF NOT EXISTS stego_revision bigint NOT NULL DEFAULT 1;
DO $disable$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid=E'managed_databases'::regclass AND tgname='stego_resource_revision') THEN
  ALTER TABLE "managed_databases" DISABLE TRIGGER stego_resource_revision;
 END IF;
 END; $disable$;
ALTER TABLE "managed_databases" ADD COLUMN IF NOT EXISTS stego_cleanup jsonb NOT NULL DEFAULT '{}';
DO $owners$ BEGIN
 IF EXISTS (SELECT 1 FROM "managed_databases" WHERE jsonb_typeof(stego_cleanup) IS DISTINCT FROM 'object') THEN
  RAISE EXCEPTION 'invalid stored cleanup state';
 END IF;
 IF EXISTS (SELECT 1 FROM "managed_databases" WHERE stego_cleanup - ARRAY[E'provider']::text[] <> '{}'::jsonb) THEN
  RAISE EXCEPTION 'cleanup owners cannot be removed from retained resources';
 END IF;
 END; $owners$;
DO $upgrade$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid=E'managed_databases'::regclass AND t.tgname='stego_resource_revision' AND p.prosrc IS DISTINCT FROM E'
BEGIN
 IF TG_OP = ''DELETE'' THEN
  RAISE EXCEPTION ''versioned resource history cannot be removed'' USING ERRCODE = ''23514'';
 END IF;
 IF TG_OP = ''INSERT'' THEN
  NEW.stego_revision := 1;
 ELSE
  IF NEW.id COLLATE "C" IS DISTINCT FROM OLD.id COLLATE "C" THEN
   RAISE EXCEPTION ''resource identity is immutable'' USING ERRCODE = ''23514'';
  END IF;
  IF OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
   RAISE EXCEPTION ''resource deletion cannot be reversed'' USING ERRCODE = ''23514'';
  END IF;
  NEW.stego_revision := OLD.stego_revision + 1;
 END IF;

 IF TG_OP = ''INSERT'' THEN
  NEW.stego_cleanup := E''{"provider":false}''::jsonb;
 ELSE
  IF NEW.deleted_at IS NULL OR OLD.deleted_at IS NULL OR
   (to_jsonb(NEW) - ARRAY[''stego_revision'',''stego_generation'',''stego_observations'',''stego_cleanup'',''updated_time'']) IS DISTINCT FROM
   (to_jsonb(OLD) - ARRAY[''stego_revision'',''stego_generation'',''stego_observations'',''stego_cleanup'',''updated_time'']) THEN
   NEW.stego_cleanup := E''{"provider":false}''::jsonb;
  ELSE
   IF jsonb_typeof(NEW.stego_cleanup) IS DISTINCT FROM ''object'' THEN
    RAISE EXCEPTION ''invalid cleanup state'' USING ERRCODE = ''23514'';
   END IF;
   IF NOT (NEW.stego_cleanup ?& ARRAY[E''provider'']::text[]) OR NEW.stego_cleanup - ARRAY[E''provider'']::text[] <> ''{}''::jsonb OR
    EXISTS (SELECT 1 FROM jsonb_each(NEW.stego_cleanup) WHERE jsonb_typeof(value) IS DISTINCT FROM ''boolean'') THEN
    RAISE EXCEPTION ''invalid cleanup owners or observations'' USING ERRCODE = ''23514'';
   END IF;
  END IF;
 END IF;
 -- cleanup fields 1b1890fd7e415cbddb13996214006cdf0b7ad2e6d9fc8aec01c45ea00ce739ab
 RETURN NEW;
END;
') THEN
  UPDATE "managed_databases" SET stego_revision=stego_revision+1, stego_cleanup=E'{"provider":false}'::jsonb;
 END IF;
 END; $upgrade$;
UPDATE "managed_databases" SET stego_cleanup=E'{"provider":false}'::jsonb || stego_cleanup WHERE NOT (stego_cleanup ?& ARRAY[E'provider']::text[]);
CREATE OR REPLACE FUNCTION "stego_revision_ccb53472b4e3b3c58a852539"() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $stego$
BEGIN
 IF TG_OP = 'DELETE' THEN
  RAISE EXCEPTION 'versioned resource history cannot be removed' USING ERRCODE = '23514';
 END IF;
 IF TG_OP = 'INSERT' THEN
  NEW.stego_revision := 1;
 ELSE
  IF NEW.id COLLATE "C" IS DISTINCT FROM OLD.id COLLATE "C" THEN
   RAISE EXCEPTION 'resource identity is immutable' USING ERRCODE = '23514';
  END IF;
  IF OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
   RAISE EXCEPTION 'resource deletion cannot be reversed' USING ERRCODE = '23514';
  END IF;
  NEW.stego_revision := OLD.stego_revision + 1;
 END IF;

 IF TG_OP = 'INSERT' THEN
  NEW.stego_cleanup := E'{"provider":false}'::jsonb;
 ELSE
  IF NEW.deleted_at IS NULL OR OLD.deleted_at IS NULL OR
   (to_jsonb(NEW) - ARRAY['stego_revision','stego_generation','stego_observations','stego_cleanup','updated_time']) IS DISTINCT FROM
   (to_jsonb(OLD) - ARRAY['stego_revision','stego_generation','stego_observations','stego_cleanup','updated_time']) THEN
   NEW.stego_cleanup := E'{"provider":false}'::jsonb;
  ELSE
   IF jsonb_typeof(NEW.stego_cleanup) IS DISTINCT FROM 'object' THEN
    RAISE EXCEPTION 'invalid cleanup state' USING ERRCODE = '23514';
   END IF;
   IF NOT (NEW.stego_cleanup ?& ARRAY[E'provider']::text[]) OR NEW.stego_cleanup - ARRAY[E'provider']::text[] <> '{}'::jsonb OR
    EXISTS (SELECT 1 FROM jsonb_each(NEW.stego_cleanup) WHERE jsonb_typeof(value) IS DISTINCT FROM 'boolean') THEN
    RAISE EXCEPTION 'invalid cleanup owners or observations' USING ERRCODE = '23514';
   END IF;
  END IF;
 END IF;
 -- cleanup fields 1b1890fd7e415cbddb13996214006cdf0b7ad2e6d9fc8aec01c45ea00ce739ab
 RETURN NEW;
END;
$stego$;
DROP TRIGGER IF EXISTS stego_resource_revision ON "managed_databases";
CREATE TRIGGER stego_resource_revision BEFORE INSERT OR UPDATE OR DELETE ON "managed_databases" FOR EACH ROW EXECUTE FUNCTION "stego_revision_ccb53472b4e3b3c58a852539"();
ALTER TABLE "gateways" ADD COLUMN IF NOT EXISTS stego_revision bigint NOT NULL DEFAULT 1;
DO $disable$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid=E'gateways'::regclass AND tgname='stego_resource_revision') THEN
  ALTER TABLE "gateways" DISABLE TRIGGER stego_resource_revision;
 END IF;
 END; $disable$;
ALTER TABLE "gateways" ADD COLUMN IF NOT EXISTS stego_generation bigint NOT NULL DEFAULT 1, ADD COLUMN IF NOT EXISTS stego_observations jsonb NOT NULL DEFAULT '{}';
DO $owners$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_attribute WHERE attrelid=E'gateways'::regclass AND attname='stego_cleanup' AND NOT attisdropped) THEN
  IF EXISTS (SELECT 1 FROM "gateways" WHERE stego_cleanup <> '{}'::jsonb) THEN
   RAISE EXCEPTION 'cleanup owners cannot be removed from retained resources';
  END IF;
 END IF;
 END; $owners$;
DO $upgrade$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid=E'gateways'::regclass AND t.tgname='stego_resource_revision' AND p.prosrc IS DISTINCT FROM E'
BEGIN
 IF TG_OP = ''DELETE'' THEN
  RAISE EXCEPTION ''versioned resource history cannot be removed'' USING ERRCODE = ''23514'';
 END IF;
 IF TG_OP = ''INSERT'' THEN
  NEW.stego_revision := 1;
 ELSE
  IF NEW.id COLLATE "C" IS DISTINCT FROM OLD.id COLLATE "C" THEN
   RAISE EXCEPTION ''resource identity is immutable'' USING ERRCODE = ''23514'';
  END IF;
  IF OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
   RAISE EXCEPTION ''resource deletion cannot be reversed'' USING ERRCODE = ''23514'';
  END IF;
  NEW.stego_revision := OLD.stego_revision + 1;
 END IF;
 IF TG_OP = ''INSERT'' THEN
  NEW.stego_generation := 1; NEW.stego_observations := ''{}''::jsonb;
 ELSE
  NEW.stego_generation := OLD.stego_generation;
  IF to_jsonb(NEW."name") IS DISTINCT FROM to_jsonb(OLD."name") OR to_jsonb(NEW."cluster_id") IS DISTINCT FROM to_jsonb(OLD."cluster_id") OR to_jsonb(NEW."release_id") IS DISTINCT FROM to_jsonb(OLD."release_id") OR to_jsonb(NEW."database_id") IS DISTINCT FROM to_jsonb(OLD."database_id") OR to_jsonb(NEW."namespace") IS DISTINCT FROM to_jsonb(OLD."namespace") OR to_jsonb(NEW."external_dns") IS DISTINCT FROM to_jsonb(OLD."external_dns") OR to_jsonb(NEW."tls_mode") IS DISTINCT FROM to_jsonb(OLD."tls_mode") OR to_jsonb(NEW."service_type") IS DISTINCT FROM to_jsonb(OLD."service_type") OR to_jsonb(NEW."image") IS DISTINCT FROM to_jsonb(OLD."image") OR to_jsonb(NEW."supervisor_image") IS DISTINCT FROM to_jsonb(OLD."supervisor_image") OR to_jsonb(NEW."server_dns_names") IS DISTINCT FROM to_jsonb(OLD."server_dns_names") OR to_jsonb(NEW."route_address") IS DISTINCT FROM to_jsonb(OLD."route_address") OR to_jsonb(NEW."oidc") IS DISTINCT FROM to_jsonb(OLD."oidc") OR to_jsonb(NEW."route") IS DISTINCT FROM to_jsonb(OLD."route") OR to_jsonb(NEW."credential_driver") IS DISTINCT FROM to_jsonb(OLD."credential_driver") OR NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
   NEW.stego_generation := OLD.stego_generation + 1;
   NEW.stego_observations := OLD.stego_observations;
  END IF;
 END IF;
 -- generation contract 0505c2098325b3e84580b9c05c524603c66676bb2be3bad1891917ba6eecf981
 RETURN NEW;
END;
') THEN
  UPDATE "gateways" SET stego_revision=stego_revision+1, stego_generation=stego_generation+1, stego_observations='{}'::jsonb;
 END IF;
 END; $upgrade$;
CREATE OR REPLACE FUNCTION "stego_revision_a74e503354fdd464eff1440f"() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $stego$
BEGIN
 IF TG_OP = 'DELETE' THEN
  RAISE EXCEPTION 'versioned resource history cannot be removed' USING ERRCODE = '23514';
 END IF;
 IF TG_OP = 'INSERT' THEN
  NEW.stego_revision := 1;
 ELSE
  IF NEW.id COLLATE "C" IS DISTINCT FROM OLD.id COLLATE "C" THEN
   RAISE EXCEPTION 'resource identity is immutable' USING ERRCODE = '23514';
  END IF;
  IF OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
   RAISE EXCEPTION 'resource deletion cannot be reversed' USING ERRCODE = '23514';
  END IF;
  NEW.stego_revision := OLD.stego_revision + 1;
 END IF;
 IF TG_OP = 'INSERT' THEN
  NEW.stego_generation := 1; NEW.stego_observations := '{}'::jsonb;
 ELSE
  NEW.stego_generation := OLD.stego_generation;
  IF to_jsonb(NEW."name") IS DISTINCT FROM to_jsonb(OLD."name") OR to_jsonb(NEW."cluster_id") IS DISTINCT FROM to_jsonb(OLD."cluster_id") OR to_jsonb(NEW."release_id") IS DISTINCT FROM to_jsonb(OLD."release_id") OR to_jsonb(NEW."database_id") IS DISTINCT FROM to_jsonb(OLD."database_id") OR to_jsonb(NEW."namespace") IS DISTINCT FROM to_jsonb(OLD."namespace") OR to_jsonb(NEW."external_dns") IS DISTINCT FROM to_jsonb(OLD."external_dns") OR to_jsonb(NEW."tls_mode") IS DISTINCT FROM to_jsonb(OLD."tls_mode") OR to_jsonb(NEW."service_type") IS DISTINCT FROM to_jsonb(OLD."service_type") OR to_jsonb(NEW."image") IS DISTINCT FROM to_jsonb(OLD."image") OR to_jsonb(NEW."supervisor_image") IS DISTINCT FROM to_jsonb(OLD."supervisor_image") OR to_jsonb(NEW."server_dns_names") IS DISTINCT FROM to_jsonb(OLD."server_dns_names") OR to_jsonb(NEW."route_address") IS DISTINCT FROM to_jsonb(OLD."route_address") OR to_jsonb(NEW."oidc") IS DISTINCT FROM to_jsonb(OLD."oidc") OR to_jsonb(NEW."route") IS DISTINCT FROM to_jsonb(OLD."route") OR to_jsonb(NEW."credential_driver") IS DISTINCT FROM to_jsonb(OLD."credential_driver") OR NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
   NEW.stego_generation := OLD.stego_generation + 1;
   NEW.stego_observations := OLD.stego_observations;
  END IF;
 END IF;
 -- generation contract 0505c2098325b3e84580b9c05c524603c66676bb2be3bad1891917ba6eecf981
 RETURN NEW;
END;
$stego$;
DROP TRIGGER IF EXISTS stego_resource_revision ON "gateways";
CREATE TRIGGER stego_resource_revision BEFORE INSERT OR UPDATE OR DELETE ON "gateways" FOR EACH ROW EXECUTE FUNCTION "stego_revision_a74e503354fdd464eff1440f"();
COMMIT;
