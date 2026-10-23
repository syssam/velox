-- script: main
-- velox:lock-timeout 5s

ALTER TABLE `users` ADD COLUMN `seen_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6);
