-- script: main

-- review: add-index: adds index users.users_org_id_created_at; building it scans the table
CREATE INDEX "users_org_id_created_at" ON "users" ("org_id", "created_at");
DROP INDEX "users_org_id";
