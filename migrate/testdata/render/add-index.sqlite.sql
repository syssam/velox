-- script: main

-- review: add-index: adds index users.user_created_at; building it scans the table
CREATE INDEX "user_created_at" ON "users" ("created_at");
