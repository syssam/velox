-- script: main
-- velox:lock-timeout 5s

ALTER INDEX "users_by_name" RENAME TO "users_name_idx";
