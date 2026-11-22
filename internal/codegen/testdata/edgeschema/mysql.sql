CREATE TABLE `courses` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE `learners` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE `enrollments` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `role` enum('owner','member') NOT NULL DEFAULT 'member',
  `joined_at` datetime(6) NOT NULL,
  `learner_id` bigint NOT NULL DEFAULT 0,
  `course_id` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  INDEX `enrollments_courses_course` (`course_id`),
  UNIQUE INDEX `enrollments_enrollment_learner_id_course_id` (`learner_id`, `course_id`),
  CONSTRAINT `enrollments_courses_course` FOREIGN KEY (`course_id`) REFERENCES `courses` (`id`),
  CONSTRAINT `enrollments_learners_learner` FOREIGN KEY (`learner_id`) REFERENCES `learners` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
