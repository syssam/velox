-- script: main
-- velox:foreign-keys off
-- velox:allow drop-enum-value posts.posts_status_check
-- velox:allow drop-enum-value users.users_status_check

-- review: sqlite-rebuild: drops check posts_status_check, adds check posts_status_check; SQLite rebuilds table posts: it copies every row into a new table; drop-enum-value: removes 'banned' from check posts.posts_status_check; rows that hold it fail or change; add-check: adds check posts.posts_status_check; it fails if any row breaks it
CREATE TABLE "velox_new_posts" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "author_id" integer NOT NULL,
  "title" text NOT NULL,
  "status" text NOT NULL DEFAULT 'active',
  CONSTRAINT "posts_author_id_fkey" FOREIGN KEY ("author_id") REFERENCES "users" ("id") ON DELETE CASCADE,
  CONSTRAINT "posts_status_check" CHECK ("status" IN ('active', 'pending'))
);
INSERT INTO "velox_new_posts" ("id", "author_id", "title", "status") SELECT "id", "author_id", "title", "status" FROM "posts";
DELETE FROM sqlite_sequence WHERE name = 'velox_new_posts';
INSERT INTO sqlite_sequence (name, seq) SELECT 'velox_new_posts', seq FROM sqlite_sequence WHERE name = 'posts';
DROP TABLE "posts";
ALTER TABLE "velox_new_posts" RENAME TO "posts";
CREATE INDEX "posts_author_id" ON "posts" ("author_id");
-- review: sqlite-rebuild: drops check users_status_check, adds check users_status_check; SQLite rebuilds table users: it copies every row into a new table; drop-enum-value: removes 'banned' from check users.users_status_check; rows that hold it fail or change; add-check: adds check users.users_status_check; it fails if any row breaks it
CREATE TABLE "velox_new_users" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "org_id" integer NOT NULL,
  "name" text NOT NULL,
  "email" text NOT NULL,
  "bio" text,
  "role" text NOT NULL DEFAULT 'viewer',
  "status" text NOT NULL DEFAULT 'active',
  "logins" integer NOT NULL DEFAULT 0,
  "created_at" datetime NOT NULL,
  "mentor_id" integer,
  CONSTRAINT "user_mentor" FOREIGN KEY ("mentor_id") REFERENCES "users" ("id"),
  CONSTRAINT "users_org_id_fkey" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id"),
  CONSTRAINT "users_role_check" CHECK ("role" IN ('admin', 'viewer')),
  CONSTRAINT "users_status_check" CHECK ("status" IN ('active', 'pending'))
);
INSERT INTO "velox_new_users" ("id", "org_id", "name", "email", "bio", "role", "status", "logins", "created_at", "mentor_id") SELECT "id", "org_id", "name", "email", "bio", "role", "status", "logins", "created_at", "mentor_id" FROM "users";
DELETE FROM sqlite_sequence WHERE name = 'velox_new_users';
INSERT INTO sqlite_sequence (name, seq) SELECT 'velox_new_users', seq FROM sqlite_sequence WHERE name = 'users';
DROP TABLE "users";
ALTER TABLE "velox_new_users" RENAME TO "users";
CREATE INDEX "users_by_name" ON "users" ("name");
CREATE UNIQUE INDEX "users_email_unique" ON "users" ("email");
CREATE INDEX "users_mentor_id" ON "users" ("mentor_id");
CREATE INDEX "users_org_id" ON "users" ("org_id");
