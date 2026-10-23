-- script: main
-- velox:lock-timeout 5s

ALTER TABLE `users` DROP FOREIGN KEY `users_org_id_fkey`;
-- review: rename-table: renames table users to accounts; code that uses the old name breaks while old and new versions run together
RENAME TABLE `users` TO `accounts`;
ALTER TABLE `accounts` RENAME INDEX `users_email_unique` TO `accounts_email_unique`, RENAME INDEX `users_mentor_id` TO `accounts_mentor_id`, RENAME INDEX `users_org_id` TO `accounts_org_id`;
ALTER TABLE `accounts` ADD CONSTRAINT `accounts_org_id_fkey` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`);
