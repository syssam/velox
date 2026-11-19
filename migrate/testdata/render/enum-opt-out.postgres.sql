-- script: main
-- velox:lock-timeout 5s
-- velox:allow alter-type users.role
-- velox:drops users.role:type="users_role"

-- review: alter-type: changes column users.role from "users_role" to text, which can fail or change values
ALTER TABLE "users" ALTER COLUMN "role" DROP DEFAULT, ALTER COLUMN "role" TYPE text USING "role"::text, ALTER COLUMN "role" SET DEFAULT 'viewer';
DROP TYPE "users_role";
