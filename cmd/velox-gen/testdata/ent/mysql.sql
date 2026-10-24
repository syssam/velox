-- The tables ent's migrate makes for schema/, as ent writes them on MySQL.
CREATE TABLE `users` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  `email` varchar(255) NOT NULL UNIQUE,
  `role` enum('admin','member') NOT NULL,
  PRIMARY KEY (`id`),
  INDEX `user_name` (`name`)
) CHARSET utf8mb4 COLLATE utf8mb4_bin;
CREATE TABLE `posts` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `title` varchar(255) NOT NULL,
  `user_posts` bigint NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `posts_users_posts` FOREIGN KEY (`user_posts`) REFERENCES `users` (`id`) ON DELETE SET NULL
) CHARSET utf8mb4 COLLATE utf8mb4_bin;
