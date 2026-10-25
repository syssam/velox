-- script: main
-- velox:allow add-unique users.users_name_key

-- review: add-unique: adds unique index users.users_name_key; it fails if rows hold duplicates
CREATE UNIQUE INDEX "users_name_key" ON "users" ("name");
