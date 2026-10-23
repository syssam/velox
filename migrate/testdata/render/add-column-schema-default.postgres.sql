-- script: main
-- velox:lock-timeout 5s

-- review: volatile-default: adds column users.seen_at with default CURRENT_TIMESTAMP; if the expression is volatile, PostgreSQL rewrites the table under an exclusive lock
ALTER TABLE "users" ADD COLUMN "seen_at" timestamp with time zone NOT NULL DEFAULT CURRENT_TIMESTAMP;
