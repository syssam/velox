-- script: main
-- velox:foreign-keys off
-- velox:allow drop-column accounts.email

DROP INDEX "users_email_unique";
-- review: rename-table: renames table users to accounts; code that uses the old name breaks while old and new versions run together
ALTER TABLE "users" RENAME TO "accounts";
-- review: sqlite-rebuild: renames constraint users_org_id_fkey to accounts_org_id_fkey; SQLite rebuilds table accounts: it copies every row into a new table; sqlite-rebuild: renames constraint users_role_check to accounts_role_check; SQLite rebuilds table accounts: it copies every row into a new table; sqlite-rebuild: renames constraint users_status_check to accounts_status_check; SQLite rebuilds table accounts: it copies every row into a new table; drop-column: drops column accounts.email and its data; sqlite-rebuild: drops column accounts.email, which a key, index or constraint uses; SQLite rebuilds table accounts: it copies every row into a new table
CREATE TABLE "velox_new_accounts" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "org_id" integer NOT NULL,
  "name" text NOT NULL,
  "bio" text,
  "role" text NOT NULL DEFAULT 'viewer',
  "status" text NOT NULL DEFAULT 'active',
  "logins" integer NOT NULL DEFAULT 0,
  "created_at" datetime NOT NULL,
  "mentor_id" integer,
  CONSTRAINT "accounts_org_id_fkey" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id"),
  CONSTRAINT "user_mentor" FOREIGN KEY ("mentor_id") REFERENCES "accounts" ("id"),
  CONSTRAINT "accounts_role_check" CHECK ("role" IN ('admin', 'viewer')),
  CONSTRAINT "accounts_status_check" CHECK ("status" IN ('active', 'banned', 'pending'))
);
INSERT INTO "velox_new_accounts" ("id", "org_id", "name", "bio", "role", "status", "logins", "created_at", "mentor_id") SELECT "id", "org_id", "name", "bio", "role", "status", "logins", "created_at", "mentor_id" FROM "accounts";
DELETE FROM sqlite_sequence WHERE name = 'velox_new_accounts';
INSERT INTO sqlite_sequence (name, seq) SELECT 'velox_new_accounts', seq FROM sqlite_sequence WHERE name = 'accounts';
DROP TABLE "accounts";
ALTER TABLE "velox_new_accounts" RENAME TO "accounts";
CREATE INDEX "accounts_mentor_id" ON "accounts" ("mentor_id");
CREATE INDEX "accounts_org_id" ON "accounts" ("org_id");
CREATE INDEX "users_by_name" ON "accounts" ("name");
