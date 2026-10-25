-- script: main
-- velox:foreign-keys off

-- review: rename-column: renames column users.role to kind; code that uses the old name breaks while old and new versions run together; sqlite-rebuild: renames constraint users_role_check to users_kind_check; SQLite rebuilds table users: it copies every row into a new table
CREATE TABLE "velox_new_users" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "org_id" integer NOT NULL,
  "name" text NOT NULL,
  "email" text NOT NULL,
  "bio" text,
  "kind" text NOT NULL DEFAULT 'viewer',
  "status" text NOT NULL DEFAULT 'active',
  "logins" integer NOT NULL DEFAULT 0,
  "created_at" datetime NOT NULL,
  "mentor_id" integer,
  CONSTRAINT "user_mentor" FOREIGN KEY ("mentor_id") REFERENCES "users" ("id"),
  CONSTRAINT "users_orgs_users" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id"),
  CONSTRAINT "users_kind_check" CHECK ("kind" IN ('admin', 'viewer')),
  CONSTRAINT "users_status_check" CHECK ("status" IN ('active', 'banned', 'pending'))
);
INSERT INTO "velox_new_users" ("id", "org_id", "name", "email", "bio", "kind", "status", "logins", "created_at", "mentor_id") SELECT "id", "org_id", "name", "email", "bio", "role", "status", "logins", "created_at", "mentor_id" FROM "users";
DELETE FROM sqlite_sequence WHERE name = 'velox_new_users';
INSERT INTO sqlite_sequence (name, seq) SELECT 'velox_new_users', seq FROM sqlite_sequence WHERE name = 'users';
DROP TABLE "users";
ALTER TABLE "velox_new_users" RENAME TO "users";
CREATE INDEX "users_by_name" ON "users" ("name");
CREATE UNIQUE INDEX "users_email_key" ON "users" ("email");
CREATE INDEX "users_mentor_id" ON "users" ("mentor_id");
CREATE INDEX "users_org_id" ON "users" ("org_id");
