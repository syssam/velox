-- script: main
-- velox:lock-timeout 5s

ALTER TABLE `posts` DROP FOREIGN KEY `posts_author_id_fkey`;
-- review: add-foreign-key: adds foreign key posts_author_id_fkey; it fails if rows have no match in users
ALTER TABLE `posts` ADD CONSTRAINT `posts_author_id_fkey` FOREIGN KEY (`author_id`) REFERENCES `users` (`id`) ON DELETE RESTRICT;
