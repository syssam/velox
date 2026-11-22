-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column users.logins
-- velox:drops users.users_user_org_id_logins

-- review: drop-index: drops index users.users_user_org_id_logins; queries that used it get slower; unindexed-fk: foreign key column users.org_id has no index that leads with it: With, Has and joins through it, and deletes in orgs, scan users; declare an index, or -allow unindexed-fk=users.org_id
DROP INDEX "users_user_org_id_logins";
-- review: drop-column: drops column users.logins and its data
ALTER TABLE "users" DROP COLUMN "logins";
