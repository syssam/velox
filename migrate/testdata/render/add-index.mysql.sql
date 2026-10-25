-- script: main
-- velox:lock-timeout 5s

-- review: add-index: adds index users.user_created_at; building it scans the table
ALTER TABLE `users` ADD INDEX `user_created_at` (`created_at`), ALGORITHM=INPLACE, LOCK=NONE;
