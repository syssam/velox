-- script: main

-- review: rename-column: renames column users.bio to about; code that uses the old name breaks while old and new versions run together
ALTER TABLE "users" RENAME COLUMN "bio" TO "about";
DROP INDEX "users_email_246fe31f";
CREATE INDEX "users_email_b06eb344" ON "users" ("email") WHERE "about" <> 'bio';
DROP INDEX "users_name_186f43ef";
CREATE INDEX "users_name_c4755694" ON "users" ("name") WHERE about IS NOT NULL;
