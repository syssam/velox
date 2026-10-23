-- script: main
-- velox:lock-timeout 5s
-- velox:allow add-required-column users.nickname

-- review: add-required-column: adds column users.nickname as NOT NULL with no default; it fails if the table has rows
ALTER TABLE `users` ADD COLUMN `nickname` varchar(255) NOT NULL;
