-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.logins
-- velox:drops users.users_user_org_id_logins

-- review: drop-index: drops index users.users_user_org_id_logins; queries that used it get slower
DROP INDEX "users_user_org_id_logins";
-- review: drop-column: drops column users.logins and its data
ALTER TABLE "users" DROP COLUMN "logins";
