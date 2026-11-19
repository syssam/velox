-- script: main
-- velox:lock-timeout 5s

ALTER TABLE "users" ALTER COLUMN "name" DROP DEFAULT, ALTER COLUMN "name" DROP NOT NULL;
