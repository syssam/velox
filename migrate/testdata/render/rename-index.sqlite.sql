-- script: main

DROP INDEX "users_by_name";
CREATE INDEX "users_name_idx" ON "users" ("name");
