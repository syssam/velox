-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column accounts.email

-- review: drop-index: drops index users.users_email_unique; queries that used it get slower
ALTER TABLE `users` DROP INDEX `users_email_unique`, ALGORITHM=INPLACE, LOCK=NONE;
-- review: rename-table: renames table users to accounts; code that uses the old name breaks while old and new versions run together
RENAME TABLE `users` TO `accounts`;
ALTER TABLE `accounts` RENAME INDEX `users_mentor_id` TO `accounts_mentor_id`, RENAME INDEX `users_org_id` TO `accounts_org_id`, DROP FOREIGN KEY `users_org_id_fkey`, ADD CONSTRAINT `accounts_org_id_fkey` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`);
-- review: drop-column: drops column accounts.email and its data
ALTER TABLE `accounts` DROP COLUMN `email`;
