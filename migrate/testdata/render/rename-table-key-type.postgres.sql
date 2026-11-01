-- script: main
-- velox:lock-timeout 5s
-- velox:drops accounts.org_id:type=integer
-- velox:drops orgs.id:type=integer

ALTER TABLE "users" DROP CONSTRAINT "users_orgs_users";
-- review: rename-table: renames table users to accounts; code that uses the old name breaks while old and new versions run together
ALTER TABLE "users" RENAME TO "accounts";
ALTER SEQUENCE "users_id_seq" RENAME TO "accounts_id_seq";
ALTER INDEX "users_email_key" RENAME TO "accounts_email_key";
ALTER INDEX "users_mentor_id" RENAME TO "accounts_mentor_id";
ALTER INDEX "users_org_id" RENAME TO "accounts_org_id";
ALTER TABLE "accounts" RENAME CONSTRAINT "users_pkey" TO "accounts_pkey";
ALTER TABLE "posts" RENAME CONSTRAINT "posts_users_posts" TO "posts_accounts_posts";
ALTER TYPE "users_role" RENAME TO "accounts_role";
-- review: widen-type: widens column accounts.org_id from integer to bigint; the table is rewritten under an exclusive lock
ALTER TABLE "accounts" ALTER COLUMN "org_id" TYPE bigint USING "org_id"::bigint;
-- review: widen-type: widens column orgs.id from integer to bigint; the table is rewritten under an exclusive lock
ALTER TABLE "orgs" ALTER COLUMN "id" TYPE bigint USING "id"::bigint;
-- script: online
-- velox:no-transaction

-- velox:unit
ALTER TABLE "accounts" DROP CONSTRAINT IF EXISTS "accounts_orgs_users";
ALTER TABLE "accounts" ADD CONSTRAINT "accounts_orgs_users" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id") NOT VALID;
ALTER TABLE "accounts" VALIDATE CONSTRAINT "accounts_orgs_users";
