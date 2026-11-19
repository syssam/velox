-- script: main
-- velox:lock-timeout 5s

-- review: add-index: adds index users.users_user_org_id_created_at; building it scans the table
ALTER TABLE `users` ADD INDEX `users_user_org_id_created_at` (`org_id`, `created_at`), DROP INDEX `users_orgs_users`, ALGORITHM=INPLACE, LOCK=NONE;
