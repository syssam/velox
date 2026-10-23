-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.logins

-- review: drop-index: drops index users.users_name_logins_unique; queries that used it get slower
ALTER TABLE `users` DROP INDEX `users_name_logins_unique`, ALGORITHM=INPLACE, LOCK=NONE;
-- review: drop-column: drops column users.logins and its data
ALTER TABLE `users` DROP COLUMN `logins`;
