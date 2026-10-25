-- script: main

-- review: add-index: adds index users.users_org_id; building it scans the table
CREATE INDEX "users_org_id" ON "users" ("org_id");
-- review: drop-index: drops index users.user_org_id_created_at; queries that used it get slower
DROP INDEX "user_org_id_created_at";
