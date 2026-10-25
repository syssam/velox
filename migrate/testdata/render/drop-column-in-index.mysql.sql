-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.logins

-- review: drop-column: drops column users.logins and its data
ALTER TABLE `users` DROP COLUMN `logins`;
-- review: add-index: adds index users.users_orgs_users; building it scans the table; drop-index: drops index users.user_org_id_logins; queries that used it get slower
ALTER TABLE `users` ADD INDEX `users_orgs_users` (`org_id`), DROP INDEX `user_org_id_logins`, ALGORITHM=INPLACE, LOCK=NONE;
