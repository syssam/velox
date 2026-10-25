-- script: main
-- velox:lock-timeout 5s

CREATE TABLE `orgs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE `users` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `org_id` bigint NOT NULL,
  `name` varchar(255) NOT NULL COMMENT 'the display name',
  `email` varchar(120) NOT NULL,
  `bio` varchar(255) NULL,
  `role` enum('admin','viewer') NOT NULL DEFAULT 'viewer',
  `status` enum('active','banned','pending') NOT NULL DEFAULT 'active',
  `logins` int NOT NULL DEFAULT 0,
  `created_at` datetime(6) NOT NULL,
  `mentor_id` bigint NULL,
  PRIMARY KEY (`id`),
  INDEX `users_by_name` (`name`),
  UNIQUE INDEX `users_email_unique` (`email`),
  INDEX `users_mentor_id` (`mentor_id`),
  INDEX `users_org_id` (`org_id`),
  CONSTRAINT `user_mentor` FOREIGN KEY (`mentor_id`) REFERENCES `users` (`id`),
  CONSTRAINT `users_org_id_fkey` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE `posts` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `author_id` bigint NOT NULL,
  `title` varchar(200) NOT NULL,
  `status` enum('active','banned','pending') NOT NULL DEFAULT 'active',
  PRIMARY KEY (`id`),
  INDEX `posts_author_id` (`author_id`),
  CONSTRAINT `posts_author_id_fkey` FOREIGN KEY (`author_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE `alphas` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `beta_id` bigint NULL,
  PRIMARY KEY (`id`),
  INDEX `alphas_beta_id` (`beta_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE `beta` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `alpha_id` bigint NULL,
  PRIMARY KEY (`id`),
  INDEX `beta_alpha_id` (`alpha_id`),
  CONSTRAINT `beta_alpha_id_fkey` FOREIGN KEY (`alpha_id`) REFERENCES `alphas` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
ALTER TABLE `alphas` ADD CONSTRAINT `alphas_beta_id_fkey` FOREIGN KEY (`beta_id`) REFERENCES `beta` (`id`);
