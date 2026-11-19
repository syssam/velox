-- script: main
-- velox:lock-timeout 5s
-- velox:allow add-required-column users.joined_at

-- review: add-required-column: adds column users.joined_at as NOT NULL with no default; it fails if the table has rows: give the field a Default or make it Nillable, or allow it only on an empty table
ALTER TABLE `users` ADD COLUMN `joined_at` datetime(6) NOT NULL;
