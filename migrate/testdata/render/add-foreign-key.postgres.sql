-- script: online
-- velox:no-transaction

-- velox:unit
-- review: add-foreign-key: adds foreign key posts_users_reviewer; it fails if rows have no match in users
ALTER TABLE "posts" DROP CONSTRAINT IF EXISTS "posts_users_reviewer";
ALTER TABLE "posts" ADD CONSTRAINT "posts_users_reviewer" FOREIGN KEY ("reviewer_id") REFERENCES "users" ("id") ON DELETE SET NULL NOT VALID;
ALTER TABLE "posts" VALIDATE CONSTRAINT "posts_users_reviewer";
