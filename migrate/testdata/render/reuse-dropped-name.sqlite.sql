-- script: main
-- velox:allow drop-table posts

DROP INDEX "shared_idx";
-- review: add-index: adds index users.shared_idx; building it scans the table
CREATE INDEX "shared_idx" ON "users" ("created_at");
-- review: drop-table: drops table posts and its rows
DROP TABLE "posts";
