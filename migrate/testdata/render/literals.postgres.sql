-- script: enum
-- velox:no-transaction
-- velox:lock-timeout 5s

-- velox:unit
ALTER TYPE "users_role" ADD VALUE IF NOT EXISTS 'o''neil';
-- script: main
-- velox:lock-timeout 5s

ALTER TABLE "users" ADD COLUMN "motto" character varying NOT NULL DEFAULT 'it''s a \ test';
COMMENT ON COLUMN "users"."motto" IS 'it''s a \ comment';
