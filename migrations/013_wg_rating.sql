CREATE TABLE IF NOT EXISTS `wind_wg_raiting` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `spot_id` int unsigned NOT NULL DEFAULT 0,
  `month` tinyint unsigned NOT NULL DEFAULT 0,
  `spot_name` varchar(255) NOT NULL DEFAULT '',
  `country_name` varchar(255) NOT NULL DEFAULT '',
  `score` smallint unsigned NOT NULL DEFAULT 0,
  `8+ Bft` tinyint unsigned NOT NULL DEFAULT 0,
  `7 Bft` tinyint unsigned NOT NULL DEFAULT 0,
  `6 Bft` tinyint unsigned NOT NULL DEFAULT 0,
  `5 Bft` tinyint unsigned NOT NULL DEFAULT 0,
  `4 Bft` tinyint unsigned NOT NULL DEFAULT 0,
  `3 Bft` tinyint unsigned NOT NULL DEFAULT 0,
  `2- Bft` tinyint unsigned NOT NULL DEFAULT 0,
  `day_temp` tinyint DEFAULT NULL,
  `night_temp` tinyint DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_wg_raiting_spot` (`spot_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `spots_details` (
  `spot_id` int unsigned NOT NULL DEFAULT 0,
  `lat` float NOT NULL DEFAULT 0,
  `lon` float NOT NULL DEFAULT 0,
  `alt` float DEFAULT 0,
  `temp` float DEFAULT 0,
  PRIMARY KEY (`spot_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `spots_raiting` (
  `spot_id` int unsigned NOT NULL DEFAULT 0,
  `spot_name` varchar(255) NOT NULL DEFAULT '',
  `country_name` varchar(255) NOT NULL DEFAULT '',
  `score` int unsigned NOT NULL DEFAULT 0,
  `8+ Bft` int unsigned NOT NULL DEFAULT 0,
  `7 Bft` int unsigned NOT NULL DEFAULT 0,
  `6 Bft` int unsigned NOT NULL DEFAULT 0,
  `5 Bft` int unsigned NOT NULL DEFAULT 0,
  `4 Bft` int unsigned NOT NULL DEFAULT 0,
  `3 Bft` int unsigned NOT NULL DEFAULT 0,
  `2- Bft` int unsigned NOT NULL DEFAULT 0,
  PRIMARY KEY (`spot_id`),
  KEY `idx_spots_raiting_country` (`country_name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
