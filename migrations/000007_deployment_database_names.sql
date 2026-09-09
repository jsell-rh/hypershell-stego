-- Apply before the deployment placement API starts.
-- A database name can contain a 255-byte Gateway name plus gw- and -db.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
ALTER TABLE managed_databases ALTER COLUMN name TYPE varchar(261);
COMMIT;
