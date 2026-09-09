BEGIN;
ALTER TABLE "gateways" ADD COLUMN IF NOT EXISTS stego_revision bigint NOT NULL DEFAULT 1;
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
 RETURN NEW;
END;
$stego$;
DROP TRIGGER IF EXISTS stego_resource_revision ON "gateways";
CREATE TRIGGER stego_resource_revision BEFORE INSERT OR UPDATE OR DELETE ON "gateways" FOR EACH ROW EXECUTE FUNCTION "stego_revision_a74e503354fdd464eff1440f"();
COMMIT;
