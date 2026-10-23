-- script: main
-- velox:lock-timeout 5s
-- velox:allow reorder-enum users_role

-- review: reorder-enum: changes the order of the values of enum type users_role; rows that hold them are rewritten
ALTER TYPE "users_role" RENAME TO "users_role_velox_old";
CREATE TYPE "users_role" AS ENUM ('viewer', 'admin');
ALTER TABLE "users" ALTER COLUMN "role" DROP DEFAULT, ALTER COLUMN "role" TYPE "users_role" USING "role"::text::"users_role", ALTER COLUMN "role" SET DEFAULT 'viewer';
DROP TYPE "users_role_velox_old";
