-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-enum-value case_status

-- review: drop-enum-value: removes 'banned' from enum type case_status; rows that hold it fail or change
ALTER TYPE "case_status" RENAME TO "case_status_velox_old";
CREATE TYPE "case_status" AS ENUM ('active', 'pending');
ALTER TABLE "posts" ALTER COLUMN "status" DROP DEFAULT, ALTER COLUMN "status" TYPE "case_status" USING "status"::text::"case_status", ALTER COLUMN "status" SET DEFAULT 'active';
ALTER TABLE "users" ALTER COLUMN "status" DROP DEFAULT, ALTER COLUMN "status" TYPE "case_status" USING "status"::text::"case_status", ALTER COLUMN "status" SET DEFAULT 'active';
DROP TYPE "case_status_velox_old";
