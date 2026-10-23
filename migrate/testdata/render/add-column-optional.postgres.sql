-- script: main
-- velox:lock-timeout 5s

ALTER TABLE "users" ADD COLUMN "nickname" text NOT NULL DEFAULT '';
