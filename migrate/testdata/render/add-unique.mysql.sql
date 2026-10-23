-- script: main
-- velox:lock-timeout 5s
-- velox:allow add-unique users.users_name_unique

-- review: add-unique: adds unique index users.users_name_unique; it fails if rows hold duplicates
ALTER TABLE `users` ADD UNIQUE INDEX `users_name_unique` (`name`), ALGORITHM=INPLACE, LOCK=NONE;
