-- script: main
-- velox:lock-timeout 5s

ALTER TABLE `users` MODIFY COLUMN `name` varchar(255) NOT NULL COMMENT 'the name shown';
