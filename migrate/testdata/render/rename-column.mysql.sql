-- script: main
-- velox:lock-timeout 5s

-- review: rename-column: renames column users.name to full_name; code that uses the old name breaks while old and new versions run together
ALTER TABLE `users` RENAME COLUMN `name` TO `full_name`;
