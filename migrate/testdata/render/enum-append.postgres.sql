-- script: enum
-- velox:no-transaction
-- velox:lock-timeout 5s

-- velox:unit
ALTER TYPE "users_role" ADD VALUE IF NOT EXISTS 'owner';
