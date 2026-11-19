-- script: main
-- velox:lock-timeout 5s
-- velox:drops "users.email:type=character varying(120)"

ALTER TABLE "users" ALTER COLUMN "email" DROP DEFAULT, ALTER COLUMN "email" TYPE character varying(255), ALTER COLUMN "email" SET DEFAULT '';
