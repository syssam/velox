-- script: main

-- review: add-index: adds index users.users_user_created_at; building it scans the table
CREATE INDEX "users_user_created_at" ON "users" ("created_at");
