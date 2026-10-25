-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.logins

-- review: drop-index: drops index users.user_name_logins; queries that used it get slower
DROP INDEX "user_name_logins";
-- review: drop-column: drops column users.logins and its data
ALTER TABLE "users" DROP COLUMN "logins";
