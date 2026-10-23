-- script: main
-- velox:lock-timeout 5s

ALTER TABLE `users` RENAME INDEX `users_by_name` TO `users_name_idx`;
