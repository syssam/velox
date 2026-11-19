-- script: main

-- review: drop-index: drops index users.users_user_org_id_created_at; queries that used it get slower
DROP INDEX "users_user_org_id_created_at";
