-- script: main
-- velox:lock-timeout 5s
-- velox:drops posts.posts_reviewer_id

-- review: drop-foreign-key: drops foreign key posts_users_reviewer; rows are no longer checked against users
ALTER TABLE "posts" DROP CONSTRAINT "posts_users_reviewer";
-- script: online
-- velox:no-transaction
-- velox:drops posts.posts_reviewer_id

-- velox:unit
-- review: drop-index: drops index posts.posts_reviewer_id; queries that used it get slower
DROP INDEX CONCURRENTLY IF EXISTS "posts_reviewer_id";
