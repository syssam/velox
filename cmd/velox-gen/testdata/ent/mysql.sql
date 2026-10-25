-- The tables ent's migrate makes for schema/, as ent writes them on MySQL.
CREATE TABLE `users` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  `email` varchar(255) NOT NULL UNIQUE,
  `role` enum('admin','member') NOT NULL,
  PRIMARY KEY (`id`),
  INDEX `user_name` (`name`)
) CHARSET utf8mb4 COLLATE utf8mb4_bin;
CREATE UNIQUE INDEX `user_email` ON `users` (`email`);
CREATE TABLE `posts` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `title` varchar(255) NOT NULL,
  `reviewed_by_an_editor_before_the_post_was_published_on` bigint NULL,
  `user_posts` bigint NULL,
  `user_drafts_written_but_never_published_by_this_user_so_far` bigint NULL,
  PRIMARY KEY (`id`),
  INDEX `post_title_reviewed_by_an_edito_610afc49e3060dc9162738c5b5b2de3d` (`title`, `reviewed_by_an_editor_before_the_post_was_published_on`),
  CONSTRAINT `posts_users_posts` FOREIGN KEY (`user_posts`) REFERENCES `users` (`id`) ON DELETE SET NULL,
  CONSTRAINT `posts_users_drafts_written_but__f5ecc73789cbf6208f1deb12fd406c67` FOREIGN KEY (`user_drafts_written_but_never_published_by_this_user_so_far`) REFERENCES `users` (`id`) ON DELETE SET NULL
) CHARSET utf8mb4 COLLATE utf8mb4_bin;
CREATE TABLE `audit_entries_kept_for_every_change_an_editor_makes_to_posts` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `note` varchar(255) NOT NULL,
  PRIMARY KEY (`id`)
) CHARSET utf8mb4 COLLATE utf8mb4_bin;
