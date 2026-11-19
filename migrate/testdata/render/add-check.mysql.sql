-- script: main
-- velox:lock-timeout 5s

-- review: add-check: adds check users.users_logins_positive; it fails if any row breaks it; MySQL copies the table
ALTER TABLE `users` ADD CONSTRAINT `users_logins_positive` CHECK (logins >= 0);
