-- script: online
-- velox:no-transaction
-- velox:drops users.users_user_org_id_created_at

-- velox:unit
-- review: drop-index: drops index users.users_user_org_id_created_at; queries that used it get slower
DROP INDEX CONCURRENTLY IF EXISTS "users_user_org_id_created_at";
