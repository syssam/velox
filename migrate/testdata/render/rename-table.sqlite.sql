-- script: main

-- review: rename-table: renames table users to accounts; code that uses the old name breaks while old and new versions run together
ALTER TABLE "users" RENAME TO "accounts";
DROP INDEX "users_by_name";
CREATE INDEX "accounts_users_by_name" ON "accounts" ("name");
DROP INDEX "users_email_key";
CREATE UNIQUE INDEX "accounts_email_key" ON "accounts" ("email");
DROP INDEX "users_mentor_id";
CREATE INDEX "accounts_mentor_id" ON "accounts" ("mentor_id");
DROP INDEX "users_org_id";
CREATE INDEX "accounts_org_id" ON "accounts" ("org_id");
