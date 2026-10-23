-- script: main
-- velox:lock-timeout 5s

ALTER TABLE `users` ADD COLUMN `nickname` varchar(255) NULL;
