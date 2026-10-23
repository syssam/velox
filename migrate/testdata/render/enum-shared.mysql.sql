-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-enum-value posts.status
-- velox:allow drop-enum-value users.status

-- review: drop-enum-value: removes 'banned' from column posts.status; rows that hold it fail or change
ALTER TABLE `posts` MODIFY COLUMN `status` enum('active','pending') NOT NULL DEFAULT 'active';
-- review: drop-enum-value: removes 'banned' from column users.status; rows that hold it fail or change
ALTER TABLE `users` MODIFY COLUMN `status` enum('active','pending') NOT NULL DEFAULT 'active';
