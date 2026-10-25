-- script: online
-- velox:no-transaction

-- velox:unit
-- review: add-index: adds index users.user_created_at; building it scans the table
DROP INDEX CONCURRENTLY IF EXISTS "user_created_at";
CREATE INDEX CONCURRENTLY "user_created_at" ON "users" ("created_at");
