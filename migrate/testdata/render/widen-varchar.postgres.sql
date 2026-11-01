-- script: main
-- velox:lock-timeout 5s

ALTER TABLE "users" ALTER COLUMN "email" TYPE character varying(255);
