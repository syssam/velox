-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-table posts

ALTER TABLE "posts" DROP CONSTRAINT "shared_fk";
DROP INDEX "shared_idx";
ALTER TABLE "users" RENAME CONSTRAINT "user_mentor" TO "shared_fk";
-- review: drop-table: drops table posts and its rows
DROP TABLE "posts";
-- script: online
-- velox:no-transaction

-- velox:unit
-- review: add-index: adds index users.shared_idx; building it scans the table
DROP INDEX CONCURRENTLY IF EXISTS "shared_idx";
CREATE INDEX CONCURRENTLY "shared_idx" ON "users" ("created_at");
