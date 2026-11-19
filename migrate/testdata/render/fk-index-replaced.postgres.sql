-- script: online
-- velox:no-transaction
-- velox:drops users.users_org_id

-- velox:unit
-- review: add-index: adds index users.users_user_org_id_created_at; building it scans the table
DROP INDEX CONCURRENTLY IF EXISTS "users_user_org_id_created_at";
CREATE INDEX CONCURRENTLY "users_user_org_id_created_at" ON "users" ("org_id", "created_at");
-- velox:unit
DROP INDEX CONCURRENTLY IF EXISTS "users_org_id";
