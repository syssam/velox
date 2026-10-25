-- script: main
-- velox:foreign-keys off

DROP INDEX "posts_reviewer_id";
-- review: sqlite-rebuild: drops foreign key posts_users_reviewer; SQLite rebuilds table posts: it copies every row into a new table
CREATE TABLE "velox_new_posts" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "author_id" integer NOT NULL,
  "title" text NOT NULL,
  "status" text NOT NULL DEFAULT 'active',
  "reviewer_id" integer,
  CONSTRAINT "posts_users_posts" FOREIGN KEY ("author_id") REFERENCES "users" ("id") ON DELETE CASCADE,
  CONSTRAINT "posts_status_check" CHECK ("status" IN ('active', 'banned', 'pending'))
);
INSERT INTO "velox_new_posts" ("id", "author_id", "title", "status", "reviewer_id") SELECT "id", "author_id", "title", "status", "reviewer_id" FROM "posts";
DELETE FROM sqlite_sequence WHERE name = 'velox_new_posts';
INSERT INTO sqlite_sequence (name, seq) SELECT 'velox_new_posts', seq FROM sqlite_sequence WHERE name = 'posts';
DROP TABLE "posts";
ALTER TABLE "velox_new_posts" RENAME TO "posts";
CREATE INDEX "posts_author_id" ON "posts" ("author_id");
