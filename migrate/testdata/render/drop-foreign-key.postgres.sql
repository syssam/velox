-- script: main
-- velox:lock-timeout 5s

-- review: drop-foreign-key: drops foreign key posts_reviewer_id_fkey; rows are no longer checked against users
ALTER TABLE "posts" DROP CONSTRAINT "posts_reviewer_id_fkey";
-- script: online
-- velox:no-transaction

-- velox:unit
-- review: drop-index: drops index posts.posts_reviewer_id; queries that used it get slower
DROP INDEX CONCURRENTLY IF EXISTS "posts_reviewer_id";
