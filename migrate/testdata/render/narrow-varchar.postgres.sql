-- script: main
-- velox:lock-timeout 5s
-- velox:allow alter-type users.email

-- review: alter-type: changes column users.email from character varying(120) to character varying(64), which can fail or change values
ALTER TABLE "users" ALTER COLUMN "email" TYPE character varying(64) USING "email"::character varying(64);
