-- script: main
-- velox:lock-timeout 5s

-- review: zero-default: adds column users.nickname as NOT NULL with default ''; existing rows get '': give the field a Default or make it Nillable if that is not their value
ALTER TABLE `users` ADD COLUMN `nickname` varchar(255) NOT NULL DEFAULT '';
