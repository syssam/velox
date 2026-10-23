-- script: main
-- velox:lock-timeout 5s

-- review: add-index: adds index users.users_org_id; building it scans the table; drop-index: drops index users.users_org_id_created_at; queries that used it get slower
ALTER TABLE `users` ADD INDEX `users_org_id` (`org_id`), DROP INDEX `users_org_id_created_at`, ALGORITHM=INPLACE, LOCK=NONE;
