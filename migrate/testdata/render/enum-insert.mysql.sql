-- script: main
-- velox:lock-timeout 5s
-- velox:allow reorder-enum users.role
-- velox:drops users.role:type

-- review: reorder-enum: changes the order of the values of column users.role; rows that hold them are rewritten
ALTER TABLE `users` MODIFY COLUMN `role` enum('admin','editor','viewer') NOT NULL DEFAULT 'viewer';
