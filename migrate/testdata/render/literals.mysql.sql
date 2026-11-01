-- script: main
-- velox:lock-timeout 5s
-- velox:drops users.role:type=enum('admin','viewer')

ALTER TABLE `users` ADD COLUMN `motto` varchar(255) NOT NULL DEFAULT 'it''s a \\ test' COMMENT 'it''s a \\ comment', MODIFY COLUMN `role` enum('admin','viewer','o''neil') NOT NULL DEFAULT 'viewer';
