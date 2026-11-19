-- script: main
-- velox:lock-timeout 5s

ALTER TABLE `users` DROP CHECK `users_role_check`;
-- review: rename-column: renames column users.role to kind; code that uses the old name breaks while old and new versions run together
ALTER TABLE `users` RENAME COLUMN `role` TO `kind`;
-- review: add-check: adds check users.users_kind_check; it fails if any row breaks it; MySQL copies the table
ALTER TABLE `users` ADD CONSTRAINT `users_kind_check` CHECK (`kind` IN ('admin', 'viewer'));
