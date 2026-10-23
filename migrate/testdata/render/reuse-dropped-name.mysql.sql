-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-table posts

ALTER TABLE `posts` DROP FOREIGN KEY `shared_fk`;
ALTER TABLE `users` DROP FOREIGN KEY `user_mentor`, ADD CONSTRAINT `shared_fk` FOREIGN KEY (`mentor_id`) REFERENCES `users` (`id`);
-- review: add-index: adds index users.shared_idx; building it scans the table
ALTER TABLE `users` ADD INDEX `shared_idx` (`created_at`), ALGORITHM=INPLACE, LOCK=NONE;
-- review: drop-table: drops table posts and its rows
DROP TABLE `posts`;
