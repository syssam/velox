-- script: main
-- velox:lock-timeout 5s
-- velox:allow alter-type users.role
-- velox:drops users.role:type=enum('admin','viewer')

-- review: alter-type: changes column users.role from enum('admin','viewer') to varchar(16), which can fail or change values
ALTER TABLE `users` MODIFY COLUMN `role` varchar(16) NOT NULL DEFAULT 'viewer';
