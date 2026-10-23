-- script: main
-- velox:lock-timeout 5s

-- review: add-index: adds index posts.posts_reviewer_id; building it scans the table
ALTER TABLE `posts` ADD INDEX `posts_reviewer_id` (`reviewer_id`), ALGORITHM=INPLACE, LOCK=NONE;
-- review: add-foreign-key: adds foreign key posts_reviewer_id_fkey; it fails if rows have no match in users
ALTER TABLE `posts` ADD CONSTRAINT `posts_reviewer_id_fkey` FOREIGN KEY (`reviewer_id`) REFERENCES `users` (`id`);
