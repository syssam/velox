-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.logins

-- review: drop-column: drops column users.logins and its data
ALTER TABLE `users` DROP COLUMN `logins`;
-- review: add-index: adds index users.users_org_id; building it scans the table; drop-index: drops index users.users_org_id_logins; queries that used it get slower
ALTER TABLE `users` ADD INDEX `users_org_id` (`org_id`), DROP INDEX `users_org_id_logins`, ALGORITHM=INPLACE, LOCK=NONE;
