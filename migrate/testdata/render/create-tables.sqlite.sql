-- script: main

CREATE TABLE "orgs" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "name" text NOT NULL
);
CREATE TABLE "users" (
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
  CONSTRAINT "users_status_check" CHECK ("status" IN ('active', 'banned', 'pending'))
);
CREATE INDEX "users_by_name" ON "users" ("name");
CREATE UNIQUE INDEX "users_email_unique" ON "users" ("email");
CREATE INDEX "users_mentor_id" ON "users" ("mentor_id");
CREATE INDEX "users_org_id" ON "users" ("org_id");
CREATE TABLE "posts" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "author_id" integer NOT NULL,
  "title" text NOT NULL,
  "status" text NOT NULL DEFAULT 'active',
  CONSTRAINT "posts_author_id_fkey" FOREIGN KEY ("author_id") REFERENCES "users" ("id") ON DELETE CASCADE,
  CONSTRAINT "posts_status_check" CHECK ("status" IN ('active', 'banned', 'pending'))
);
CREATE INDEX "posts_author_id" ON "posts" ("author_id");
CREATE TABLE "alphas" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "beta_id" integer,
  CONSTRAINT "alphas_beta_id_fkey" FOREIGN KEY ("beta_id") REFERENCES "beta" ("id")
);
CREATE INDEX "alphas_beta_id" ON "alphas" ("beta_id");
CREATE TABLE "beta" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "alpha_id" integer,
  CONSTRAINT "beta_alpha_id_fkey" FOREIGN KEY ("alpha_id") REFERENCES "alphas" ("id")
);
CREATE INDEX "beta_alpha_id" ON "beta" ("alpha_id");
