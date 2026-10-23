-- script: main
-- velox:lock-timeout 5s
-- velox:allow alter-type users.email

-- review: alter-type: changes column users.email from varchar(120) to varchar(64), which can fail or change values
ALTER TABLE `users` MODIFY COLUMN `email` varchar(64) NOT NULL;
