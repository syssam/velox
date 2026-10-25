-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.logins

ALTER TABLE `users` DROP FOREIGN KEY `users_orgs_users`;
-- review: drop-index: drops index users.user_org_id_logins; queries that used it get slower
ALTER TABLE `users` DROP INDEX `user_org_id_logins`, ALGORITHM=INPLACE, LOCK=NONE;
-- review: drop-column: drops column users.logins and its data
ALTER TABLE `users` DROP COLUMN `logins`;
-- review: add-index: adds index users.users_orgs_users; building it scans the table
ALTER TABLE `users` ADD INDEX `users_orgs_users` (`org_id`), ALGORITHM=INPLACE, LOCK=NONE;
ALTER TABLE `users` ADD CONSTRAINT `users_orgs_users` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`);
