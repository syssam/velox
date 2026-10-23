-- script: main
-- velox:lock-timeout 5s

-- review: drop-index: drops index users.users_by_name; queries that used it get slower
ALTER TABLE `users` DROP INDEX `users_by_name`, ALGORITHM=INPLACE, LOCK=NONE;
