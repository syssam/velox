-- script: online
-- velox:no-transaction
-- velox:drops users.users_user_org_id_created_at

-- velox:unit
-- review: add-index: adds index users.users_org_id; building it scans the table
DROP INDEX CONCURRENTLY IF EXISTS "users_org_id";
CREATE INDEX CONCURRENTLY "users_org_id" ON "users" ("org_id");
-- velox:unit
-- review: drop-index: drops index users.users_user_org_id_created_at; queries that used it get slower
DROP INDEX CONCURRENTLY IF EXISTS "users_user_org_id_created_at";
