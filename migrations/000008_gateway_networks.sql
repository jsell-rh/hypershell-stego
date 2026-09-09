-- Stop the API and apply earlier migrations first.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
CREATE TABLE IF NOT EXISTS gateway_networks (
 id text PRIMARY KEY,
 created_time timestamptz,
 updated_time timestamptz,
 deleted_at timestamptz,
 name varchar(255) NOT NULL,
 topology varchar(64),
 tunnel_mode varchar(64),
 hub_gateway_id varchar(255),
 status varchar(255),
 CONSTRAINT chk_gateway_networks_name CHECK (length(name) >= 1)
);
CREATE INDEX IF NOT EXISTS idx_gateway_networks_deleted_at ON gateway_networks(deleted_at);
UPDATE roles SET permissions=permissions || '{"gateway_networks":["create","read","update","delete","list"]}'::jsonb, updated_time=now()
 WHERE name='platform:admin' AND NOT permissions @> '{"gateway_networks":["create","read","update","delete","list"]}'::jsonb;
UPDATE roles SET permissions=permissions || '{"gateway_networks":["read","list"]}'::jsonb, updated_time=now()
 WHERE name='gateway:creator' AND NOT permissions @> '{"gateway_networks":["read","list"]}'::jsonb;
COMMIT;
