-- script: main
-- velox:foreign-keys off
-- velox:allow add-required-column users.joined_at

-- review: add-required-column: adds column users.joined_at as NOT NULL with no default; it fails if the table has rows: give the field a Default or make it Nillable, or allow it only on an empty table; sqlite-rebuild: adds column users.joined_at, which SQLite cannot add in place; SQLite rebuilds table users: it copies every row into a new table
CREATE TABLE "velox_new_users" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "org_id" integer NOT NULL DEFAULT 0,
  "name" text NOT NULL DEFAULT '',
  "email" text NOT NULL DEFAULT '',
  "bio" text,
  "role" text NOT NULL DEFAULT 'viewer',
  "status" text NOT NULL DEFAULT 'active',
  "logins" integer NOT NULL DEFAULT 0,
  "created_at" datetime(9) NOT NULL,
  "mentor_id" integer,
  "joined_at" datetime(9) NOT NULL,
  CONSTRAINT "user_mentor" FOREIGN KEY ("mentor_id") REFERENCES "users" ("id") ON DELETE SET NULL,
  CONSTRAINT "users_orgs_users" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id")
);
INSERT INTO "velox_new_users" ("id", "org_id", "name", "email", "bio", "role", "status", "logins", "created_at", "mentor_id") SELECT "id", "org_id", "name", "email", "bio", "role", "status", "logins", "created_at", "mentor_id" FROM "users";
DELETE FROM sqlite_sequence WHERE name = 'velox_new_users';
INSERT INTO sqlite_sequence (name, seq) SELECT 'velox_new_users', seq FROM sqlite_sequence WHERE name = 'users';
DROP TABLE "users";
ALTER TABLE "velox_new_users" RENAME TO "users";
CREATE INDEX "users_by_name" ON "users" ("name");
CREATE UNIQUE INDEX "users_email_key" ON "users" ("email");
