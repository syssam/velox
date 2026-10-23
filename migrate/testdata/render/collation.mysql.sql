-- script: main
-- velox:lock-timeout 5s
-- velox:allow convert-collation users

-- review: convert-collation: converts table users from collation utf8mb4_0900_ai_ci to utf8mb4_bin, a rewrite that can make values equal that were not
ALTER TABLE `users` CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
