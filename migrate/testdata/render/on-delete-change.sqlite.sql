-- script: main
-- velox:foreign-keys off

-- review: sqlite-rebuild: drops foreign key posts_author_id_fkey, adds foreign key posts_author_id_fkey; SQLite rebuilds table posts: it copies every row into a new table; add-foreign-key: adds foreign key posts_author_id_fkey; it fails if rows have no match in users
CREATE TABLE "velox_new_posts" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "author_id" integer NOT NULL,
  "title" text NOT NULL,
  "status" text NOT NULL DEFAULT 'active',
  CONSTRAINT "posts_author_id_fkey" FOREIGN KEY ("author_id") REFERENCES "users" ("id") ON DELETE RESTRICT,
  CONSTRAINT "posts_status_check" CHECK ("status" IN ('active', 'banned', 'pending'))
);
INSERT INTO "velox_new_posts" ("id", "author_id", "title", "status") SELECT "id", "author_id", "title", "status" FROM "posts";
DELETE FROM sqlite_sequence WHERE name = 'velox_new_posts';
INSERT INTO sqlite_sequence (name, seq) SELECT 'velox_new_posts', seq FROM sqlite_sequence WHERE name = 'posts';
DROP TABLE "posts";
ALTER TABLE "velox_new_posts" RENAME TO "posts";
CREATE INDEX "posts_author_id" ON "posts" ("author_id");
