-- script: main
-- velox:lock-timeout 5s

-- review: drop-foreign-key: drops foreign key posts_users_reviewer; rows are no longer checked against users
ALTER TABLE `posts` DROP FOREIGN KEY `posts_users_reviewer`;
-- review: drop-index: drops index posts.posts_users_reviewer; queries that used it get slower
ALTER TABLE `posts` DROP INDEX `posts_users_reviewer`, ALGORITHM=INPLACE, LOCK=NONE;
