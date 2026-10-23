-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.email

-- review: drop-index: drops index users.users_email_unique; queries that used it get slower
ALTER TABLE `users` DROP INDEX `users_email_unique`, ALGORITHM=INPLACE, LOCK=NONE;
-- review: drop-column: drops column users.email and its data
ALTER TABLE `users` DROP COLUMN `email`;
