-- script: main
-- velox:lock-timeout 5s
-- velox:allow add-unique users.name

-- review: add-unique: adds unique index users.name; it fails if rows hold duplicates
ALTER TABLE `users` ADD UNIQUE INDEX `name` (`name`), ALGORITHM=INPLACE, LOCK=NONE;
