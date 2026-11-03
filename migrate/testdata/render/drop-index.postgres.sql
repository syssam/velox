-- script: online
-- velox:no-transaction
-- velox:drops users.users_by_name

-- velox:unit
-- review: drop-index: drops index users.users_by_name; queries that used it get slower
DROP INDEX CONCURRENTLY IF EXISTS "users_by_name";
