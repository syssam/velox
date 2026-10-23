-- script: main
-- velox:lock-timeout 5s

ALTER TABLE `users` DROP FOREIGN KEY `users_org_id_fkey`;
-- review: widen-type: widens column orgs.id from int to bigint; the table is rewritten under an exclusive lock
ALTER TABLE `orgs` MODIFY COLUMN `id` bigint NOT NULL AUTO_INCREMENT;
-- review: widen-type: widens column users.org_id from int to bigint; the table is rewritten under an exclusive lock
ALTER TABLE `users` MODIFY COLUMN `org_id` bigint NOT NULL;
ALTER TABLE `users` ADD CONSTRAINT `users_org_id_fkey` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`);
