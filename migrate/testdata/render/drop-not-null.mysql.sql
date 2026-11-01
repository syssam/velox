-- script: main
-- velox:lock-timeout 5s
-- velox:drops users.name:not-null=not-null

ALTER TABLE `users` MODIFY COLUMN `name` varchar(255) NULL COMMENT 'the display name';
