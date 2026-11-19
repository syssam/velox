-- script: main
-- velox:lock-timeout 5s

-- review: add-index: adds index users.users_user_created_at; building it scans the table
ALTER TABLE `users` ADD INDEX `users_user_created_at` (`created_at`), ALGORITHM=INPLACE, LOCK=NONE;
