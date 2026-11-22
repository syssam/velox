-- script: main
-- velox:foreign-keys off

-- review: add-foreign-key: adds foreign key posts_users_reviewer; it fails if rows have no match in users; sqlite-rebuild: adds foreign key posts_users_reviewer; SQLite rebuilds table posts: it copies every row into a new table; unindexed-fk: foreign key column posts.reviewer_id has no index that leads with it: With, Has and joins through it, and deletes in users, scan posts; declare an index, or -allow unindexed-fk=posts.reviewer_id
CREATE TABLE "velox_new_posts" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "author_id" integer NOT NULL DEFAULT 0,
  "title" text NOT NULL DEFAULT '',
  "status" text NOT NULL DEFAULT 'active',
  "reviewer_id" integer,
  CONSTRAINT "posts_users_posts" FOREIGN KEY ("author_id") REFERENCES "users" ("id") ON DELETE CASCADE,
  CONSTRAINT "posts_users_reviewer" FOREIGN KEY ("reviewer_id") REFERENCES "users" ("id") ON DELETE SET NULL
);
INSERT INTO "velox_new_posts" ("id", "author_id", "title", "status", "reviewer_id") SELECT "id", "author_id", "title", "status", "reviewer_id" FROM "posts";
DELETE FROM sqlite_sequence WHERE name = 'velox_new_posts';
INSERT INTO sqlite_sequence (name, seq) SELECT 'velox_new_posts', seq FROM sqlite_sequence WHERE name = 'posts';
DROP TABLE "posts";
ALTER TABLE "velox_new_posts" RENAME TO "posts";
