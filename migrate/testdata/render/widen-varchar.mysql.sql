-- script: main
-- velox:lock-timeout 5s

-- review: widen-type: widens column users.email from varchar(120) to varchar(255); the table is rewritten under an exclusive lock
ALTER TABLE `users` MODIFY COLUMN `email` varchar(255) NOT NULL;
