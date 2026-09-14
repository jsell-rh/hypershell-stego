-- This SQL runs only inside the fresh-generation bootstrap transaction.
ALTER TABLE roles ADD CONSTRAINT roles_permissions_object
 CHECK (permissions IS NULL OR jsonb_typeof(permissions)='object');
ALTER TABLE role_bindings ADD CONSTRAINT role_bindings_gateway_scope CHECK (
 (scope='gateway' AND gateway_id IS NOT NULL) OR
 (scope='global' AND gateway_id IS NULL)
);
CREATE UNIQUE INDEX role_bindings_live_global_key
 ON role_bindings(role_id,user_id) WHERE deleted_at IS NULL AND scope='global';
INSERT INTO roles(id,name,display_name,description,permissions,built_in,created_time,updated_time) VALUES
 ('3J4dFWwyXQw80wNZfUyA4BCFBzY','platform:admin','Platform Administrator','Can read and delete all Gateways. Can manage placement records.','{"gateways":["read","delete"],"managed_clusters":["create","read","update","delete","list"],"gateway_releases":["create","read","update","delete","list"],"gateway_networks":["create","read","update","delete","list"]}',true,now(),now()),
 ('3J4dFTnQjvBXpaSRP28ugn8ATpb','gateway:creator','Gateway Creator','Can create Gateways and read placement records. Receives an owner grant on creation.','{"gateways":["create"],"role_bindings":["create","read","delete","list"],"managed_clusters":["read","list"],"gateway_releases":["read","list"],"gateway_networks":["read","list"]}',true,now(),now()),
 ('3J4dFUK9EDmspOqyUe20rDD4iM8','gateway:owner','Gateway Owner','Can read, change, and delete one Gateway. Can give owner and viewer access.','{"gateways":["read","update","delete"],"role_bindings":["create","read","delete","list"]}',true,now(),now()),
 ('3J4dFSkxpG300P1x43mVwHwL4AB','gateway:viewer','Gateway Viewer','Can read one Gateway.','{"gateways":["read"]}',true,now(),now());
