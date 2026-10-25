-- script: main
-- velox:lock-timeout 5s

ALTER TABLE "posts" DROP CONSTRAINT "posts_users_posts";
-- script: online
-- velox:no-transaction

-- velox:unit
-- review: add-foreign-key: adds foreign key posts_users_posts; it fails if rows have no match in users
ALTER TABLE "posts" DROP CONSTRAINT IF EXISTS "posts_users_posts";
ALTER TABLE "posts" ADD CONSTRAINT "posts_users_posts" FOREIGN KEY ("author_id") REFERENCES "users" ("id") ON DELETE RESTRICT NOT VALID;
ALTER TABLE "posts" VALIDATE CONSTRAINT "posts_users_posts";
