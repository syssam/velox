-- script: main

-- review: drop-index: drops index users.users_by_name; queries that used it get slower
DROP INDEX "users_by_name";
