-- script: main
-- velox:lock-timeout 5s
-- velox:drops orgs.id:type=int
-- velox:drops users.org_id:type=int

ALTER TABLE `users` DROP FOREIGN KEY `users_orgs_users`;
-- review: widen-type: widens column orgs.id from int to bigint; the table is rewritten under an exclusive lock
ALTER TABLE `orgs` MODIFY COLUMN `id` bigint NOT NULL AUTO_INCREMENT;
-- review: widen-type: widens column users.org_id from int to bigint; the table is rewritten under an exclusive lock
ALTER TABLE `users` MODIFY COLUMN `org_id` bigint NOT NULL DEFAULT 0;
ALTER TABLE `users` ADD CONSTRAINT `users_orgs_users` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`);
