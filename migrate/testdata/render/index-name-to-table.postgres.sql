-- script: main
-- velox:lock-timeout 5s
-- velox:drops users.tags

-- review: drop-index: drops index users.tags; queries that used it get slower
DROP INDEX "tags";
CREATE TABLE "tags" (
  "id" text NOT NULL,
  "label" text NOT NULL,
  CONSTRAINT "tags_pkey" PRIMARY KEY ("id")
);
