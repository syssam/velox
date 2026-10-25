-- script: main
-- velox:lock-timeout 5s

ALTER TABLE "users" DROP CONSTRAINT "users_orgs_users";
-- review: widen-type: widens column orgs.id from integer to bigint; the table is rewritten under an exclusive lock
ALTER TABLE "orgs" ALTER COLUMN "id" TYPE bigint USING "id"::bigint;
-- review: widen-type: widens column users.org_id from integer to bigint; the table is rewritten under an exclusive lock
ALTER TABLE "users" ALTER COLUMN "org_id" TYPE bigint USING "org_id"::bigint;
-- script: online
-- velox:no-transaction

-- velox:unit
ALTER TABLE "users" DROP CONSTRAINT IF EXISTS "users_orgs_users";
ALTER TABLE "users" ADD CONSTRAINT "users_orgs_users" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id") NOT VALID;
ALTER TABLE "users" VALIDATE CONSTRAINT "users_orgs_users";
