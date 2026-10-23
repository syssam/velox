-- script: online
-- velox:no-transaction

-- velox:unit
-- review: add-index: adds index posts.posts_reviewer_id; building it scans the table
DROP INDEX CONCURRENTLY IF EXISTS "posts_reviewer_id";
CREATE INDEX CONCURRENTLY "posts_reviewer_id" ON "posts" ("reviewer_id");
-- velox:unit
-- review: add-foreign-key: adds foreign key posts_reviewer_id_fkey; it fails if rows have no match in users
ALTER TABLE "posts" DROP CONSTRAINT IF EXISTS "posts_reviewer_id_fkey";
ALTER TABLE "posts" ADD CONSTRAINT "posts_reviewer_id_fkey" FOREIGN KEY ("reviewer_id") REFERENCES "users" ("id") NOT VALID;
ALTER TABLE "posts" VALIDATE CONSTRAINT "posts_reviewer_id_fkey";
