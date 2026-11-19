-- script: main
-- velox:lock-timeout 5s
-- velox:allow set-not-null users.bio

ALTER TABLE "users" ALTER COLUMN "bio" SET DEFAULT '';
-- script: online
-- velox:no-transaction
-- velox:allow set-not-null users.bio

-- velox:unit
-- review: set-not-null: makes column users.bio NOT NULL; it fails if any row holds NULL
ALTER TABLE "users" DROP CONSTRAINT IF EXISTS "users_bio_not_null";
ALTER TABLE "users" ADD CONSTRAINT "users_bio_not_null" CHECK ("bio" IS NOT NULL) NOT VALID;
ALTER TABLE "users" VALIDATE CONSTRAINT "users_bio_not_null";
ALTER TABLE "users" ALTER COLUMN "bio" SET NOT NULL;
ALTER TABLE "users" DROP CONSTRAINT "users_bio_not_null";
