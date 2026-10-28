-- The tables ent's migrate makes for schema/, as ent v0.14.6 writes them on SQLite.
CREATE TABLE `users` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `name` text NOT NULL, `email` text NOT NULL, `role` text NOT NULL);
CREATE UNIQUE INDEX `users_email_key` ON `users` (`email`);
CREATE INDEX `user_name` ON `users` (`name`);
CREATE UNIQUE INDEX `user_email` ON `users` (`email`);
CREATE TABLE `posts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `title` text NOT NULL, `reviewed_by_an_editor_before_the_post_was_published_on` integer NULL, `user_posts` integer NULL, `user_drafts_written_but_never_published_by_this_user_so_far` integer NULL, CONSTRAINT `posts_users_posts` FOREIGN KEY (`user_posts`) REFERENCES `users` (`id`) ON DELETE SET NULL, CONSTRAINT `posts_users_drafts_written_but__f5ecc73789cbf6208f1deb12fd406c67` FOREIGN KEY (`user_drafts_written_but_never_published_by_this_user_so_far`) REFERENCES `users` (`id`) ON DELETE SET NULL);
CREATE INDEX `post_title_reviewed_by_an_edito_610afc49e3060dc9162738c5b5b2de3d` ON `posts` (`title`, `reviewed_by_an_editor_before_the_post_was_published_on`);
CREATE TABLE `audit_entries_kept_for_every_change_an_editor_makes_to_posts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `note` text NOT NULL);
