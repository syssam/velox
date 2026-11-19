-- script: main
-- velox:lock-timeout 5s

-- review: drop-foreign-key: drops foreign key posts_users_reviewer; rows are no longer checked against users
ALTER TABLE "posts" DROP CONSTRAINT "posts_users_reviewer";
