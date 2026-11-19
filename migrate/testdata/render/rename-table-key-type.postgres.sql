-- script: main
-- velox:lock-timeout 5s
-- velox:drops accounts.org_id:type=integer
-- velox:drops orgs.id:type=integer

ALTER TABLE "users" DROP CONSTRAINT "users_orgs_users";
-- review: rename-table: renames table users to accounts; code that uses the old name breaks while old and new versions run together
ALTER TABLE "users" RENAME TO "accounts";
ALTER SEQUENCE "users_id_seq" RENAME TO "accounts_id_seq";
ALTER INDEX "users_by_name" RENAME TO "accounts_users_by_name";
ALTER INDEX "users_email_key" RENAME TO "accounts_email_key";
ALTER TABLE "accounts" RENAME CONSTRAINT "users_pkey" TO "accounts_pkey";
ALTER TABLE "posts" RENAME CONSTRAINT "posts_users_posts" TO "posts_accounts_posts";
-- review: widen-type: widens column accounts.org_id from integer to bigint; the table is rewritten under an exclusive lock
ALTER TABLE "accounts" ALTER COLUMN "org_id" DROP DEFAULT, ALTER COLUMN "org_id" TYPE bigint USING "org_id"::bigint, ALTER COLUMN "org_id" SET DEFAULT 0;
-- review: widen-type: widens column orgs.id from integer to bigint; the table is rewritten under an exclusive lock
ALTER TABLE "orgs" ALTER COLUMN "id" TYPE bigint USING "id"::bigint;
ALTER TABLE "accounts" ADD CONSTRAINT "accounts_orgs_users" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id") NOT VALID;
-- script: online
-- velox:no-transaction

-- velox:unit
ALTER TABLE "accounts" VALIDATE CONSTRAINT "accounts_orgs_users";
