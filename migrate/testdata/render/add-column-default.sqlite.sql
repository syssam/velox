-- script: main

ALTER TABLE "users" ADD COLUMN "nickname" text NOT NULL DEFAULT 'anon';
