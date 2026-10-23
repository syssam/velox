-- script: main
-- velox:lock-timeout 5s

-- review: rename-column: renames column users.bio to about; code that uses the old name breaks while old and new versions run together
ALTER TABLE "users" RENAME COLUMN "bio" TO "about";
ALTER INDEX "users_email_246fe31f" RENAME TO "users_email_b06eb344";
ALTER INDEX "users_name_186f43ef" RENAME TO "users_name_c4755694";
