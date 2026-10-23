-- script: main
-- velox:lock-timeout 5s
-- velox:allow alter-type users.role

-- review: alter-type: changes column users.role from "users_role" to text, which can fail or change values
ALTER TABLE "users" ALTER COLUMN "role" DROP DEFAULT, ALTER COLUMN "role" TYPE text USING "role"::text, ALTER COLUMN "role" SET DEFAULT 'viewer';
DROP TYPE "users_role";
-- script: online
-- velox:no-transaction

-- velox:unit
-- review: add-check: adds check users.users_role_check; it fails if any row breaks it
ALTER TABLE "users" DROP CONSTRAINT IF EXISTS "users_role_check";
ALTER TABLE "users" ADD CONSTRAINT "users_role_check" CHECK ("role" IN ('admin', 'viewer')) NOT VALID;
ALTER TABLE "users" VALIDATE CONSTRAINT "users_role_check";
