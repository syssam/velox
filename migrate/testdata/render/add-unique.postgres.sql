-- script: online
-- velox:no-transaction
-- velox:allow add-unique users.users_name_unique

-- velox:unit
-- review: add-unique: adds unique index users.users_name_unique; it fails if rows hold duplicates
DROP INDEX CONCURRENTLY IF EXISTS "users_name_unique";
CREATE UNIQUE INDEX CONCURRENTLY "users_name_unique" ON "users" ("name");
