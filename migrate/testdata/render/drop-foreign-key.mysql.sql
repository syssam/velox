-- script: main
-- velox:lock-timeout 5s

-- review: drop-foreign-key: drops foreign key posts_reviewer_id_fkey; rows are no longer checked against users
ALTER TABLE `posts` DROP FOREIGN KEY `posts_reviewer_id_fkey`;
-- review: drop-index: drops index posts.posts_reviewer_id; queries that used it get slower
ALTER TABLE `posts` DROP INDEX `posts_reviewer_id`, ALGORITHM=INPLACE, LOCK=NONE;
