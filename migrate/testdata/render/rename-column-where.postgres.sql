-- script: main
-- velox:lock-timeout 5s

-- review: rename-column: renames column users.bio to about; code that uses the old name breaks while old and new versions run together
ALTER TABLE "users" RENAME COLUMN "bio" TO "about";
