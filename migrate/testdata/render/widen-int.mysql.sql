-- script: main
-- velox:lock-timeout 5s

-- review: widen-type: widens column users.logins from int to bigint; the table is rewritten under an exclusive lock
ALTER TABLE `users` MODIFY COLUMN `logins` bigint NOT NULL DEFAULT 0;
