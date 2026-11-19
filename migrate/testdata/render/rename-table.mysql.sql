-- script: main
-- velox:lock-timeout 5s

ALTER TABLE `users` DROP FOREIGN KEY `users_orgs_users`;
ALTER TABLE `posts` DROP FOREIGN KEY `posts_users_posts`;
-- review: rename-table: renames table users to accounts; code that uses the old name breaks while old and new versions run together
RENAME TABLE `users` TO `accounts`;
ALTER TABLE `accounts` RENAME INDEX `users_by_name` TO `accounts_users_by_name`, RENAME INDEX `users_orgs_users` TO `accounts_orgs_users`;
ALTER TABLE `posts` RENAME INDEX `posts_users_posts` TO `posts_accounts_posts`;
ALTER TABLE `accounts` ADD CONSTRAINT `accounts_orgs_users` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`);
ALTER TABLE `posts` ADD CONSTRAINT `posts_accounts_posts` FOREIGN KEY (`author_id`) REFERENCES `accounts` (`id`) ON DELETE CASCADE;
