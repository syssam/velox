-- script: main
-- velox:allow add-unique users.users_name_unique

-- review: add-unique: adds unique index users.users_name_unique; it fails if rows hold duplicates
CREATE UNIQUE INDEX "users_name_unique" ON "users" ("name");
