-- script: main
-- velox:lock-timeout 5s
-- velox:allow drop-column accounts.email
-- velox:drops users.users_email_key

-- review: drop-index: drops index users.users_email_key; queries that used it get slower
DROP INDEX "users_email_key";
-- review: rename-table: renames table users to accounts; code that uses the old name breaks while old and new versions run together
ALTER TABLE "users" RENAME TO "accounts";
ALTER SEQUENCE "users_id_seq" RENAME TO "accounts_id_seq";
ALTER INDEX "users_by_name" RENAME TO "accounts_users_by_name";
ALTER INDEX "users_mentor_id" RENAME TO "accounts_mentor_id";
ALTER INDEX "users_org_id" RENAME TO "accounts_org_id";
ALTER TABLE "accounts" RENAME CONSTRAINT "users_orgs_users" TO "accounts_orgs_users";
ALTER TABLE "accounts" RENAME CONSTRAINT "users_pkey" TO "accounts_pkey";
ALTER TABLE "posts" RENAME CONSTRAINT "posts_users_posts" TO "posts_accounts_posts";
-- review: drop-column: drops column accounts.email and its data
ALTER TABLE "accounts" DROP COLUMN "email";
