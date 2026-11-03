-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.logins
-- velox:drops users.user_org_id_logins

-- review: drop-index: drops index users.user_org_id_logins; queries that used it get slower
DROP INDEX "user_org_id_logins";
-- review: drop-column: drops column users.logins and its data
ALTER TABLE "users" DROP COLUMN "logins";
-- script: online
-- velox:no-transaction
-- velox:drops users.user_org_id_logins

-- velox:unit
-- review: add-index: adds index users.users_org_id; building it scans the table
DROP INDEX CONCURRENTLY IF EXISTS "users_org_id";
CREATE INDEX CONCURRENTLY "users_org_id" ON "users" ("org_id");
