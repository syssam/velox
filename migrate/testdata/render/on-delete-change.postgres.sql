-- script: main
-- velox:lock-timeout 5s

ALTER TABLE "posts" DROP CONSTRAINT "posts_author_id_fkey";
-- script: online
-- velox:no-transaction

-- velox:unit
-- review: add-foreign-key: adds foreign key posts_author_id_fkey; it fails if rows have no match in users
ALTER TABLE "posts" DROP CONSTRAINT IF EXISTS "posts_author_id_fkey";
ALTER TABLE "posts" ADD CONSTRAINT "posts_author_id_fkey" FOREIGN KEY ("author_id") REFERENCES "users" ("id") ON DELETE RESTRICT NOT VALID;
ALTER TABLE "posts" VALIDATE CONSTRAINT "posts_author_id_fkey";
