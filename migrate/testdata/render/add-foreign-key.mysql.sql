-- script: main
-- velox:lock-timeout 5s

-- review: add-index: adds index posts.posts_users_reviewer; building it scans the table
ALTER TABLE `posts` ADD INDEX `posts_users_reviewer` (`reviewer_id`), ALGORITHM=INPLACE, LOCK=NONE;
-- review: add-foreign-key: adds foreign key posts_users_reviewer; it fails if rows have no match in users
ALTER TABLE `posts` ADD CONSTRAINT `posts_users_reviewer` FOREIGN KEY (`reviewer_id`) REFERENCES `users` (`id`);
