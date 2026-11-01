-- script: main
-- velox:lock-timeout 5s
-- velox:drops users.role:type=enum('admin','viewer')

ALTER TABLE `users` MODIFY COLUMN `role` enum('admin','viewer','owner') NOT NULL DEFAULT 'viewer';
