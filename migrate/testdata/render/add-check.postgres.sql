-- script: online
-- velox:no-transaction

-- velox:unit
-- review: add-check: adds check users.users_logins_positive; it fails if any row breaks it
ALTER TABLE "users" DROP CONSTRAINT IF EXISTS "users_logins_positive";
ALTER TABLE "users" ADD CONSTRAINT "users_logins_positive" CHECK (logins >= 0) NOT VALID;
ALTER TABLE "users" VALIDATE CONSTRAINT "users_logins_positive";
