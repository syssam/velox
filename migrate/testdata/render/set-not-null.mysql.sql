-- script: main
-- velox:lock-timeout 5s
-- velox:allow set-not-null users.bio
-- velox:drops users.bio:default=
-- velox:drops users.bio:not-null=nullable

-- review: set-not-null: makes column users.bio NOT NULL; it fails if any row holds NULL
ALTER TABLE `users` MODIFY COLUMN `bio` varchar(255) NOT NULL DEFAULT '';
