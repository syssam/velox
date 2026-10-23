-- script: main
-- velox:lock-timeout 5s

CREATE TABLE `billing`.`payers` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE `billing`.`invoices` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `state` enum('open','paid') NOT NULL DEFAULT 'open',
  `invoice_payer` bigint NOT NULL,
  PRIMARY KEY (`id`),
  INDEX `invoices_invoice_payer` (`invoice_payer`),
  CONSTRAINT `invoices_invoice_payer_fkey` FOREIGN KEY (`invoice_payer`) REFERENCES `billing`.`payers` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
