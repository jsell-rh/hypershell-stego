BEGIN;
ALTER TABLE "managed_databases" ADD COLUMN IF NOT EXISTS stego_revision bigint NOT NULL DEFAULT 1;
DO $disable$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid=E'managed_databases'::regclass AND tgname='stego_resource_revision') THEN
  ALTER TABLE "managed_databases" DISABLE TRIGGER stego_resource_revision;
 END IF;
 END; $disable$;
DO $conditions$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_catalog.pg_attribute WHERE attrelid=E'managed_databases'::regclass AND attname='stego_conditions' AND NOT attisdropped) THEN
  IF EXISTS(SELECT 1 FROM "managed_databases" WHERE stego_conditions <> '{}'::jsonb) THEN RAISE EXCEPTION 'condition owners cannot be removed from retained resources'; END IF;
 END IF;
 END; $conditions$;
ALTER TABLE "managed_databases" ADD COLUMN IF NOT EXISTS stego_cleanup jsonb NOT NULL DEFAULT '{}';
DO $owners$ BEGIN
 IF EXISTS (SELECT 1 FROM "managed_databases" WHERE jsonb_typeof(stego_cleanup) IS DISTINCT FROM 'object') THEN
  RAISE EXCEPTION 'invalid stored cleanup state';
 END IF;
 IF EXISTS (SELECT 1 FROM "managed_databases" WHERE stego_cleanup - ARRAY[E'provider']::text[] <> '{}'::jsonb) THEN
  RAISE EXCEPTION 'cleanup owners cannot be removed from retained resources';
 END IF;
 END; $owners$;
DO $targets$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_catalog.pg_attribute WHERE attrelid=E'managed_databases'::regclass AND attname='stego_cleanup_targets' AND NOT attisdropped) THEN
  IF EXISTS(SELECT 1 FROM "managed_databases" WHERE stego_cleanup_targets<>'{}'::jsonb) THEN
   RAISE EXCEPTION 'cleanup target declarations cannot be removed from retained resources';
  END IF;
 END IF;
 END; $targets$;
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
ALTER TABLE "gateways" ADD COLUMN IF NOT EXISTS stego_conditions jsonb NOT NULL DEFAULT '{}';
DO $conditions$ DECLARE NEW record; BEGIN FOR NEW IN SELECT stego_conditions,stego_generation FROM "gateways" LOOP  IF jsonb_typeof(NEW.stego_conditions) IS DISTINCT FROM 'object' OR octet_length(NEW.stego_conditions::text)>65536 OR NEW.stego_conditions - ARRAY[E'identity',E'identity_users']::text[] <> '{}'::jsonb THEN
 RAISE EXCEPTION 'invalid resource conditions' USING ERRCODE='23514'; END IF;
 IF NEW.stego_conditions ? E'identity' THEN
 IF jsonb_typeof(NEW.stego_conditions->E'identity') IS DISTINCT FROM 'object' OR (NEW.stego_conditions->E'identity') - ARRAY[E'ClientReady']::text[] <> '{}'::jsonb THEN
 RAISE EXCEPTION 'invalid condition owner state' USING ERRCODE='23514'; END IF;
 END IF;
 IF NEW.stego_conditions ? E'identity_users' THEN
 IF jsonb_typeof(NEW.stego_conditions->E'identity_users') IS DISTINCT FROM 'object' OR (NEW.stego_conditions->E'identity_users') - ARRAY[E'GrantsSynchronized']::text[] <> '{}'::jsonb THEN
 RAISE EXCEPTION 'invalid condition owner state' USING ERRCODE='23514'; END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(NEW.stego_conditions) g CROSS JOIN LATERAL jsonb_each(g.value) c
 WHERE jsonb_typeof(c.value) IS DISTINCT FROM 'object'
 OR NOT (c.value ?& ARRAY['status','reason','message','observed_generation','last_transition_time'])
 OR c.value - ARRAY['status','reason','message','observed_generation','last_transition_time']::text[] <> '{}'::jsonb
 OR jsonb_typeof(c.value->'status') IS DISTINCT FROM 'string' OR c.value->>'status' NOT IN ('True','False','Unknown')
 OR jsonb_typeof(c.value->'reason') IS DISTINCT FROM 'string' OR c.value->>'reason' !~ '^[A-Z][A-Za-z0-9]{0,62}$'
 OR jsonb_typeof(c.value->'message') IS DISTINCT FROM 'string' OR octet_length(c.value->>'message')>1024 OR c.value->>'message' ~ '[[:cntrl:]]'
 OR jsonb_typeof(c.value->'observed_generation') IS DISTINCT FROM 'number' OR c.value->>'observed_generation' !~ '^[1-9][0-9]*$'
 OR jsonb_typeof(c.value->'last_transition_time') IS DISTINCT FROM 'string') THEN
 RAISE EXCEPTION 'invalid condition value' USING ERRCODE='23514'; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(NEW.stego_conditions) g CROSS JOIN LATERAL jsonb_each(g.value) c
 WHERE (c.value->>'observed_generation')::bigint>NEW.stego_generation
 OR NOT isfinite((c.value->>'last_transition_time')::timestamptz)) THEN
 RAISE EXCEPTION 'invalid condition generation or time' USING ERRCODE='23514'; END IF;
 END LOOP; END; $conditions$;
ALTER TABLE "gateways" ADD COLUMN IF NOT EXISTS stego_cleanup jsonb NOT NULL DEFAULT '{}';
DO $owners$ BEGIN
 IF EXISTS (SELECT 1 FROM "gateways" WHERE jsonb_typeof(stego_cleanup) IS DISTINCT FROM 'object') THEN
  RAISE EXCEPTION 'invalid stored cleanup state';
 END IF;
 IF EXISTS (SELECT 1 FROM "gateways" WHERE stego_cleanup - ARRAY[E'identity',E'workload']::text[] <> '{}'::jsonb) THEN
  RAISE EXCEPTION 'cleanup owners cannot be removed from retained resources';
 END IF;
 END; $owners$;
ALTER TABLE "gateways" ADD COLUMN IF NOT EXISTS stego_cleanup_targets jsonb NOT NULL DEFAULT '{}';
DO $targets$ BEGIN
 IF EXISTS(SELECT 1 FROM "gateways") AND NOT EXISTS(
  SELECT 1 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_proc p ON p.oid=t.tgfoid
  WHERE t.tgrelid=E'gateways'::regclass AND t.tgname='stego_resource_revision' AND strpos(p.prosrc,E'-- cleanup target fields 48fcfc33e677423a56576ee81f06da25710abdb8f91254182c6a8bb63db37ca1')>0
 ) THEN
  RAISE EXCEPTION 'cleanup target history requires an explicit migration for existing resources';
 END IF;
END; $targets$;
DO $upgrade$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid=E'gateways'::regclass AND t.tgname='stego_resource_revision' AND p.prosrc IS DISTINCT FROM E'DECLARE
 target_owner text; target_field text; target_value text;
 target_state jsonb; target_reset boolean; target_keys text[];
 target_total integer := 0;

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
 IF TG_OP = ''INSERT'' THEN NEW.stego_conditions := ''{}''::jsonb; END IF;
 IF jsonb_typeof(NEW.stego_conditions) IS DISTINCT FROM ''object'' OR octet_length(NEW.stego_conditions::text)>65536 OR NEW.stego_conditions - ARRAY[E''identity'',E''identity_users'']::text[] <> ''{}''::jsonb THEN
 RAISE EXCEPTION ''invalid resource conditions'' USING ERRCODE=''23514''; END IF;
 IF NEW.stego_conditions ? E''identity'' THEN
 IF jsonb_typeof(NEW.stego_conditions->E''identity'') IS DISTINCT FROM ''object'' OR (NEW.stego_conditions->E''identity'') - ARRAY[E''ClientReady'']::text[] <> ''{}''::jsonb THEN
 RAISE EXCEPTION ''invalid condition owner state'' USING ERRCODE=''23514''; END IF;
 END IF;
 IF NEW.stego_conditions ? E''identity_users'' THEN
 IF jsonb_typeof(NEW.stego_conditions->E''identity_users'') IS DISTINCT FROM ''object'' OR (NEW.stego_conditions->E''identity_users'') - ARRAY[E''GrantsSynchronized'']::text[] <> ''{}''::jsonb THEN
 RAISE EXCEPTION ''invalid condition owner state'' USING ERRCODE=''23514''; END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(NEW.stego_conditions) g CROSS JOIN LATERAL jsonb_each(g.value) c
 WHERE jsonb_typeof(c.value) IS DISTINCT FROM ''object''
 OR NOT (c.value ?& ARRAY[''status'',''reason'',''message'',''observed_generation'',''last_transition_time''])
 OR c.value - ARRAY[''status'',''reason'',''message'',''observed_generation'',''last_transition_time'']::text[] <> ''{}''::jsonb
 OR jsonb_typeof(c.value->''status'') IS DISTINCT FROM ''string'' OR c.value->>''status'' NOT IN (''True'',''False'',''Unknown'')
 OR jsonb_typeof(c.value->''reason'') IS DISTINCT FROM ''string'' OR c.value->>''reason'' !~ ''^[A-Z][A-Za-z0-9]{0,62}$''
 OR jsonb_typeof(c.value->''message'') IS DISTINCT FROM ''string'' OR octet_length(c.value->>''message'')>1024 OR c.value->>''message'' ~ ''[[:cntrl:]]''
 OR jsonb_typeof(c.value->''observed_generation'') IS DISTINCT FROM ''number'' OR c.value->>''observed_generation'' !~ ''^[1-9][0-9]*$''
 OR jsonb_typeof(c.value->''last_transition_time'') IS DISTINCT FROM ''string'') THEN
 RAISE EXCEPTION ''invalid condition value'' USING ERRCODE=''23514''; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(NEW.stego_conditions) g CROSS JOIN LATERAL jsonb_each(g.value) c
 WHERE (c.value->>''observed_generation'')::bigint>NEW.stego_generation
 OR NOT isfinite((c.value->>''last_transition_time'')::timestamptz)) THEN
 RAISE EXCEPTION ''invalid condition generation or time'' USING ERRCODE=''23514''; END IF;

 IF TG_OP = ''INSERT'' THEN
  NEW.stego_cleanup := E''{"identity":false,"workload":false}''::jsonb;
 ELSE
  IF NEW.deleted_at IS NULL OR OLD.deleted_at IS NULL OR
   (to_jsonb(NEW) - ARRAY[''stego_revision'',''stego_generation'',''stego_observations'',''stego_cleanup'',''updated_time'',''stego_cleanup_targets'']) IS DISTINCT FROM
   (to_jsonb(OLD) - ARRAY[''stego_revision'',''stego_generation'',''stego_observations'',''stego_cleanup'',''updated_time'',''stego_cleanup_targets'']) THEN
   NEW.stego_cleanup := E''{"identity":false,"workload":false}''::jsonb;
  ELSE
   IF jsonb_typeof(NEW.stego_cleanup) IS DISTINCT FROM ''object'' THEN
    RAISE EXCEPTION ''invalid cleanup state'' USING ERRCODE = ''23514'';
   END IF;
   IF NOT (NEW.stego_cleanup ?& ARRAY[E''identity'',E''workload'']::text[]) OR NEW.stego_cleanup - ARRAY[E''identity'',E''workload'']::text[] <> ''{}''::jsonb OR
    EXISTS (SELECT 1 FROM jsonb_each(NEW.stego_cleanup) WHERE jsonb_typeof(value) IS DISTINCT FROM ''boolean'') THEN
    RAISE EXCEPTION ''invalid cleanup owners or observations'' USING ERRCODE = ''23514'';
   END IF;
  END IF;
 END IF;
 -- cleanup fields b0b625515599cc389479173f4f49e97328846358ac537208054c00da3c2c2206

 target_reset := TG_OP = ''INSERT'';
 IF TG_OP = ''INSERT'' THEN
  NEW.stego_cleanup_targets := ''{}''::jsonb;
 ELSE
  target_reset := NEW.deleted_at IS NULL OR OLD.deleted_at IS NULL OR
   (to_jsonb(NEW) - ARRAY[''stego_revision'',''stego_generation'',''stego_observations'',''stego_cleanup'',''updated_time'',''stego_cleanup_targets'']) IS DISTINCT FROM (to_jsonb(OLD) - ARRAY[''stego_revision'',''stego_generation'',''stego_observations'',''stego_cleanup'',''updated_time'',''stego_cleanup_targets'']);
  IF target_reset THEN NEW.stego_cleanup_targets := OLD.stego_cleanup_targets; END IF;
 END IF;
 IF jsonb_typeof(NEW.stego_cleanup_targets) IS DISTINCT FROM ''object'' THEN
  RAISE EXCEPTION ''invalid cleanup target state'' USING ERRCODE=''23514'';
 END IF;
 FOR target_owner,target_field IN SELECT * FROM (VALUES (E''workload'',E''cluster_id'')) AS fields(owner,field) LOOP
  target_value := to_jsonb(NEW)->>target_field;
  IF target_value IS NULL OR octet_length(target_value) NOT BETWEEN 1 AND 256 THEN
   RAISE EXCEPTION ''cleanup target must contain 1 through 256 bytes'' USING ERRCODE=''23514'';
  END IF;
  IF TG_OP = ''INSERT'' THEN target_state := ''{}''::jsonb;
  ELSE
   target_state := NEW.stego_cleanup_targets->target_owner;
   IF jsonb_typeof(target_state) IS DISTINCT FROM ''object'' OR jsonb_typeof(OLD.stego_cleanup_targets->target_owner) IS DISTINCT FROM ''object'' THEN
    RAISE EXCEPTION ''missing cleanup target history'' USING ERRCODE=''23514'';
   END IF;
   SELECT COALESCE(array_agg(key ORDER BY key COLLATE "C"),ARRAY[]::text[]) INTO target_keys FROM jsonb_object_keys(OLD.stego_cleanup_targets->target_owner) AS keys(key);
   IF NOT (target_state ?& target_keys) OR target_state - target_keys <> ''{}''::jsonb THEN
    RAISE EXCEPTION ''cleanup target history is immutable'' USING ERRCODE=''23514'';
   END IF;
  END IF;
  IF NOT (target_state ? target_value) THEN
   IF NOT target_reset THEN RAISE EXCEPTION ''current cleanup target is missing'' USING ERRCODE=''23514''; END IF;
   target_state := target_state || jsonb_build_object(target_value,false);
  END IF;
  IF target_reset THEN
   SELECT jsonb_object_agg(key,false) INTO target_state FROM jsonb_object_keys(target_state) AS keys(key);
  END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(target_state) WHERE octet_length(key) NOT BETWEEN 1 AND 256 OR jsonb_typeof(value) IS DISTINCT FROM ''boolean'') THEN
   RAISE EXCEPTION ''invalid cleanup target observation'' USING ERRCODE=''23514'';
  END IF;
  target_total := target_total + (SELECT count(*) FROM jsonb_object_keys(target_state));
  NEW.stego_cleanup_targets := jsonb_set(NEW.stego_cleanup_targets,ARRAY[target_owner],target_state);
  NEW.stego_cleanup := jsonb_set(NEW.stego_cleanup,ARRAY[target_owner],to_jsonb(NEW.deleted_at IS NOT NULL AND (SELECT bool_and(value=''true''::jsonb) FROM jsonb_each(target_state))));
 END LOOP;
 IF NOT (NEW.stego_cleanup_targets ?& ARRAY[E''workload'']::text[]) OR NEW.stego_cleanup_targets - ARRAY[E''workload'']::text[] <> ''{}''::jsonb OR target_total>128 OR octet_length(NEW.stego_cleanup_targets::text)>65536 THEN
  RAISE EXCEPTION ''invalid or excessive cleanup target history'' USING ERRCODE=''23514'';
 END IF;
 -- cleanup target fields 48fcfc33e677423a56576ee81f06da25710abdb8f91254182c6a8bb63db37ca1
 RETURN NEW;
END;
') THEN
  UPDATE "gateways" SET stego_revision=stego_revision+1, stego_generation=stego_generation+1, stego_observations='{}'::jsonb, stego_cleanup=E'{"identity":false,"workload":false}'::jsonb, stego_cleanup_targets=(SELECT jsonb_object_agg(owner.key,(SELECT jsonb_object_agg(target.key,false) FROM jsonb_object_keys(owner.value) AS target(key))) FROM jsonb_each(stego_cleanup_targets) AS owner);
 END IF;
 END; $upgrade$;
UPDATE "gateways" SET stego_cleanup=E'{"identity":false,"workload":false}'::jsonb || stego_cleanup WHERE NOT (stego_cleanup ?& ARRAY[E'identity',E'workload']::text[]);
CREATE OR REPLACE FUNCTION "stego_revision_a74e503354fdd464eff1440f"() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $stego$DECLARE
 target_owner text; target_field text; target_value text;
 target_state jsonb; target_reset boolean; target_keys text[];
 target_total integer := 0;

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
 IF TG_OP = 'INSERT' THEN NEW.stego_conditions := '{}'::jsonb; END IF;
 IF jsonb_typeof(NEW.stego_conditions) IS DISTINCT FROM 'object' OR octet_length(NEW.stego_conditions::text)>65536 OR NEW.stego_conditions - ARRAY[E'identity',E'identity_users']::text[] <> '{}'::jsonb THEN
 RAISE EXCEPTION 'invalid resource conditions' USING ERRCODE='23514'; END IF;
 IF NEW.stego_conditions ? E'identity' THEN
 IF jsonb_typeof(NEW.stego_conditions->E'identity') IS DISTINCT FROM 'object' OR (NEW.stego_conditions->E'identity') - ARRAY[E'ClientReady']::text[] <> '{}'::jsonb THEN
 RAISE EXCEPTION 'invalid condition owner state' USING ERRCODE='23514'; END IF;
 END IF;
 IF NEW.stego_conditions ? E'identity_users' THEN
 IF jsonb_typeof(NEW.stego_conditions->E'identity_users') IS DISTINCT FROM 'object' OR (NEW.stego_conditions->E'identity_users') - ARRAY[E'GrantsSynchronized']::text[] <> '{}'::jsonb THEN
 RAISE EXCEPTION 'invalid condition owner state' USING ERRCODE='23514'; END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(NEW.stego_conditions) g CROSS JOIN LATERAL jsonb_each(g.value) c
 WHERE jsonb_typeof(c.value) IS DISTINCT FROM 'object'
 OR NOT (c.value ?& ARRAY['status','reason','message','observed_generation','last_transition_time'])
 OR c.value - ARRAY['status','reason','message','observed_generation','last_transition_time']::text[] <> '{}'::jsonb
 OR jsonb_typeof(c.value->'status') IS DISTINCT FROM 'string' OR c.value->>'status' NOT IN ('True','False','Unknown')
 OR jsonb_typeof(c.value->'reason') IS DISTINCT FROM 'string' OR c.value->>'reason' !~ '^[A-Z][A-Za-z0-9]{0,62}$'
 OR jsonb_typeof(c.value->'message') IS DISTINCT FROM 'string' OR octet_length(c.value->>'message')>1024 OR c.value->>'message' ~ '[[:cntrl:]]'
 OR jsonb_typeof(c.value->'observed_generation') IS DISTINCT FROM 'number' OR c.value->>'observed_generation' !~ '^[1-9][0-9]*$'
 OR jsonb_typeof(c.value->'last_transition_time') IS DISTINCT FROM 'string') THEN
 RAISE EXCEPTION 'invalid condition value' USING ERRCODE='23514'; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(NEW.stego_conditions) g CROSS JOIN LATERAL jsonb_each(g.value) c
 WHERE (c.value->>'observed_generation')::bigint>NEW.stego_generation
 OR NOT isfinite((c.value->>'last_transition_time')::timestamptz)) THEN
 RAISE EXCEPTION 'invalid condition generation or time' USING ERRCODE='23514'; END IF;

 IF TG_OP = 'INSERT' THEN
  NEW.stego_cleanup := E'{"identity":false,"workload":false}'::jsonb;
 ELSE
  IF NEW.deleted_at IS NULL OR OLD.deleted_at IS NULL OR
   (to_jsonb(NEW) - ARRAY['stego_revision','stego_generation','stego_observations','stego_cleanup','updated_time','stego_cleanup_targets']) IS DISTINCT FROM
   (to_jsonb(OLD) - ARRAY['stego_revision','stego_generation','stego_observations','stego_cleanup','updated_time','stego_cleanup_targets']) THEN
   NEW.stego_cleanup := E'{"identity":false,"workload":false}'::jsonb;
  ELSE
   IF jsonb_typeof(NEW.stego_cleanup) IS DISTINCT FROM 'object' THEN
    RAISE EXCEPTION 'invalid cleanup state' USING ERRCODE = '23514';
   END IF;
   IF NOT (NEW.stego_cleanup ?& ARRAY[E'identity',E'workload']::text[]) OR NEW.stego_cleanup - ARRAY[E'identity',E'workload']::text[] <> '{}'::jsonb OR
    EXISTS (SELECT 1 FROM jsonb_each(NEW.stego_cleanup) WHERE jsonb_typeof(value) IS DISTINCT FROM 'boolean') THEN
    RAISE EXCEPTION 'invalid cleanup owners or observations' USING ERRCODE = '23514';
   END IF;
  END IF;
 END IF;
 -- cleanup fields b0b625515599cc389479173f4f49e97328846358ac537208054c00da3c2c2206

 target_reset := TG_OP = 'INSERT';
 IF TG_OP = 'INSERT' THEN
  NEW.stego_cleanup_targets := '{}'::jsonb;
 ELSE
  target_reset := NEW.deleted_at IS NULL OR OLD.deleted_at IS NULL OR
   (to_jsonb(NEW) - ARRAY['stego_revision','stego_generation','stego_observations','stego_cleanup','updated_time','stego_cleanup_targets']) IS DISTINCT FROM (to_jsonb(OLD) - ARRAY['stego_revision','stego_generation','stego_observations','stego_cleanup','updated_time','stego_cleanup_targets']);
  IF target_reset THEN NEW.stego_cleanup_targets := OLD.stego_cleanup_targets; END IF;
 END IF;
 IF jsonb_typeof(NEW.stego_cleanup_targets) IS DISTINCT FROM 'object' THEN
  RAISE EXCEPTION 'invalid cleanup target state' USING ERRCODE='23514';
 END IF;
 FOR target_owner,target_field IN SELECT * FROM (VALUES (E'workload',E'cluster_id')) AS fields(owner,field) LOOP
  target_value := to_jsonb(NEW)->>target_field;
  IF target_value IS NULL OR octet_length(target_value) NOT BETWEEN 1 AND 256 THEN
   RAISE EXCEPTION 'cleanup target must contain 1 through 256 bytes' USING ERRCODE='23514';
  END IF;
  IF TG_OP = 'INSERT' THEN target_state := '{}'::jsonb;
  ELSE
   target_state := NEW.stego_cleanup_targets->target_owner;
   IF jsonb_typeof(target_state) IS DISTINCT FROM 'object' OR jsonb_typeof(OLD.stego_cleanup_targets->target_owner) IS DISTINCT FROM 'object' THEN
    RAISE EXCEPTION 'missing cleanup target history' USING ERRCODE='23514';
   END IF;
   SELECT COALESCE(array_agg(key ORDER BY key COLLATE "C"),ARRAY[]::text[]) INTO target_keys FROM jsonb_object_keys(OLD.stego_cleanup_targets->target_owner) AS keys(key);
   IF NOT (target_state ?& target_keys) OR target_state - target_keys <> '{}'::jsonb THEN
    RAISE EXCEPTION 'cleanup target history is immutable' USING ERRCODE='23514';
   END IF;
  END IF;
  IF NOT (target_state ? target_value) THEN
   IF NOT target_reset THEN RAISE EXCEPTION 'current cleanup target is missing' USING ERRCODE='23514'; END IF;
   target_state := target_state || jsonb_build_object(target_value,false);
  END IF;
  IF target_reset THEN
   SELECT jsonb_object_agg(key,false) INTO target_state FROM jsonb_object_keys(target_state) AS keys(key);
  END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(target_state) WHERE octet_length(key) NOT BETWEEN 1 AND 256 OR jsonb_typeof(value) IS DISTINCT FROM 'boolean') THEN
   RAISE EXCEPTION 'invalid cleanup target observation' USING ERRCODE='23514';
  END IF;
  target_total := target_total + (SELECT count(*) FROM jsonb_object_keys(target_state));
  NEW.stego_cleanup_targets := jsonb_set(NEW.stego_cleanup_targets,ARRAY[target_owner],target_state);
  NEW.stego_cleanup := jsonb_set(NEW.stego_cleanup,ARRAY[target_owner],to_jsonb(NEW.deleted_at IS NOT NULL AND (SELECT bool_and(value='true'::jsonb) FROM jsonb_each(target_state))));
 END LOOP;
 IF NOT (NEW.stego_cleanup_targets ?& ARRAY[E'workload']::text[]) OR NEW.stego_cleanup_targets - ARRAY[E'workload']::text[] <> '{}'::jsonb OR target_total>128 OR octet_length(NEW.stego_cleanup_targets::text)>65536 THEN
  RAISE EXCEPTION 'invalid or excessive cleanup target history' USING ERRCODE='23514';
 END IF;
 -- cleanup target fields 48fcfc33e677423a56576ee81f06da25710abdb8f91254182c6a8bb63db37ca1
 RETURN NEW;
END;
$stego$;
DROP TRIGGER IF EXISTS stego_resource_revision ON "gateways";
CREATE TRIGGER stego_resource_revision BEFORE INSERT OR UPDATE OR DELETE ON "gateways" FOR EACH ROW EXECUTE FUNCTION "stego_revision_a74e503354fdd464eff1440f"();
COMMIT;
