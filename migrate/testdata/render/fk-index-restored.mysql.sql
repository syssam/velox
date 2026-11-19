-- script: main
-- velox:lock-timeout 5s

-- review: add-index: adds index users.users_orgs_users; building it scans the table; drop-index: drops index users.users_user_org_id_created_at; queries that used it get slower
ALTER TABLE `users` ADD INDEX `users_orgs_users` (`org_id`), DROP INDEX `users_user_org_id_created_at`, ALGORITHM=INPLACE, LOCK=NONE;
