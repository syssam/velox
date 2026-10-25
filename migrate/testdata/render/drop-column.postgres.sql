-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.email

-- review: drop-index: drops index users.users_email_key; queries that used it get slower
DROP INDEX "users_email_key";
-- review: drop-column: drops column users.email and its data
ALTER TABLE "users" DROP COLUMN "email";
