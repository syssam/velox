-- script: main
-- velox:lock-timeout 5s
-- velox:drops users.logins:type=integer

-- review: widen-type: widens column users.logins from integer to bigint; the table is rewritten under an exclusive lock
ALTER TABLE "users" ALTER COLUMN "logins" DROP DEFAULT, ALTER COLUMN "logins" TYPE bigint USING "logins"::bigint, ALTER COLUMN "logins" SET DEFAULT 0;
