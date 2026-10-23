-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-enum-value users_role

-- review: drop-enum-value: removes 'admin' from enum type users_role; rows that hold it fail or change
ALTER TYPE "users_role" RENAME TO "users_role_velox_old";
CREATE TYPE "users_role" AS ENUM ('viewer');
ALTER TABLE "users" ALTER COLUMN "role" DROP DEFAULT, ALTER COLUMN "role" TYPE "users_role" USING "role"::text::"users_role", ALTER COLUMN "role" SET DEFAULT 'viewer';
DROP TYPE "users_role_velox_old";
