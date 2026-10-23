-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-enum-value users_role

-- review: drop-enum-value: removes 'guest' from enum type users_role; rows that hold it fail or change
ALTER TYPE "users_role" RENAME TO "users_role_velox_old";
CREATE TYPE "users_role" AS ENUM ('admin', 'viewer');
ALTER TABLE "users" DROP CONSTRAINT "users_role_known";
DROP INDEX "users_name_30015c4f";
ALTER TABLE "users" ALTER COLUMN "role" DROP DEFAULT, ALTER COLUMN "role" TYPE "users_role" USING "role"::text::"users_role", ALTER COLUMN "role" SET DEFAULT 'viewer';
CREATE INDEX "users_name_30015c4f" ON "users" ("name") WHERE role = 'admin';
ALTER TABLE "users" ADD CONSTRAINT "users_role_known" CHECK ("role" IN ('admin', 'viewer'));
DROP TYPE "users_role_velox_old";
