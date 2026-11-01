-- script: main
-- velox:lock-timeout 5s
-- velox:allow alter-type users.role
-- velox:drops users.role:type

-- review: alter-type: changes column users.role from enum('admin','viewer') to varchar(16), which can fail or change values
ALTER TABLE `users` MODIFY COLUMN `role` varchar(16) NOT NULL DEFAULT 'viewer';
-- review: add-check: adds check users.users_role_check; it fails if any row breaks it; MySQL copies the table
ALTER TABLE `users` ADD CONSTRAINT `users_role_check` CHECK (`role` IN ('admin', 'viewer'));
