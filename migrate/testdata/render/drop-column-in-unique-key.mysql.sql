-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.logins

ALTER TABLE `users` DROP FOREIGN KEY `users_org_id_fkey`;
-- review: drop-index: drops index users.users_org_id_logins_unique; queries that used it get slower
ALTER TABLE `users` DROP INDEX `users_org_id_logins_unique`, ALGORITHM=INPLACE, LOCK=NONE;
-- review: drop-column: drops column users.logins and its data
ALTER TABLE `users` DROP COLUMN `logins`;
-- review: add-index: adds index users.users_org_id; building it scans the table
ALTER TABLE `users` ADD INDEX `users_org_id` (`org_id`), ALGORITHM=INPLACE, LOCK=NONE;
ALTER TABLE `users` ADD CONSTRAINT `users_org_id_fkey` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`);
