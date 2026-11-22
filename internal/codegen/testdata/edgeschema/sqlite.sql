CREATE TABLE "courses" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT
);
CREATE TABLE "learners" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "name" text NOT NULL DEFAULT ''
);
CREATE TABLE "enrollments" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "role" text NOT NULL DEFAULT 'member',
  "joined_at" datetime(9) NOT NULL,
  "learner_id" integer NOT NULL DEFAULT 0,
  "course_id" integer NOT NULL DEFAULT 0,
  CONSTRAINT "enrollments_courses_course" FOREIGN KEY ("course_id") REFERENCES "courses" ("id"),
  CONSTRAINT "enrollments_learners_learner" FOREIGN KEY ("learner_id") REFERENCES "learners" ("id")
);
CREATE UNIQUE INDEX "enrollments_enrollment_learner_id_course_id" ON "enrollments" ("learner_id", "course_id");
