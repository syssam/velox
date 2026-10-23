-- script: main
-- velox:lock-timeout 5s

ALTER TABLE "users" ALTER COLUMN "role" SET DEFAULT 'admin';
