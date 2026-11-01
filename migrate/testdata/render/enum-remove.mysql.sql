-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-enum-value users.role
-- velox:drops users.role:type=enum('admin','viewer')

-- review: drop-enum-value: removes 'admin' from column users.role; rows that hold it fail or change
ALTER TABLE `users` MODIFY COLUMN `role` enum('viewer') NOT NULL DEFAULT 'viewer';
