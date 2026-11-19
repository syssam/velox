-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column accounts.email

-- review: drop-index: drops index users.email; queries that used it get slower
ALTER TABLE `users` DROP INDEX `email`, ALGORITHM=INPLACE, LOCK=NONE;
ALTER TABLE `users` DROP FOREIGN KEY `users_orgs_users`;
ALTER TABLE `posts` DROP FOREIGN KEY `posts_users_posts`;
-- review: rename-table: renames table users to accounts; code that uses the old name breaks while old and new versions run together
RENAME TABLE `users` TO `accounts`;
ALTER TABLE `accounts` RENAME INDEX `users_by_name` TO `accounts_users_by_name`, RENAME INDEX `users_orgs_users` TO `accounts_orgs_users`;
ALTER TABLE `posts` RENAME INDEX `posts_users_posts` TO `posts_accounts_posts`;
-- review: drop-column: drops column accounts.email and its data
ALTER TABLE `accounts` DROP COLUMN `email`;
ALTER TABLE `accounts` ADD CONSTRAINT `accounts_orgs_users` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`);
ALTER TABLE `posts` ADD CONSTRAINT `posts_accounts_posts` FOREIGN KEY (`author_id`) REFERENCES `accounts` (`id`) ON DELETE CASCADE;
