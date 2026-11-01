-- script: main
-- velox:lock-timeout 5s
-- velox:drops "users.name:comment=the display name"

ALTER TABLE `users` MODIFY COLUMN `name` varchar(255) NOT NULL COMMENT 'the name shown';
