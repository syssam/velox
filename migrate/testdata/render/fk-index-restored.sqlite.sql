-- script: main

-- review: drop-index: drops index users.users_user_org_id_created_at; queries that used it get slower; unindexed-fk: foreign key column users.org_id has no index that leads with it: With, Has and joins through it, and deletes in orgs, scan users; declare an index, or -allow unindexed-fk=users.org_id
DROP INDEX "users_user_org_id_created_at";
