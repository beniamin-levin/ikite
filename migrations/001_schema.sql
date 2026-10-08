CREATE TABLE IF NOT EXISTS `wind_data` (
  `period` datetime NOT NULL,
  `location` varchar(32) NOT NULL,
  `wind` double NOT NULL DEFAULT 0,
  `gust` double NOT NULL DEFAULT 0,
  `wind_dir` double NOT NULL DEFAULT 0,
  `temp` double DEFAULT NULL,
  PRIMARY KEY (`period`, `location`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `wind_data_log` (
  `period` datetime NOT NULL,
  `location` varchar(32) NOT NULL,
  `log` mediumtext NOT NULL,
  PRIMARY KEY (`period`, `location`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `settings` (
  `s_key` varchar(64) NOT NULL,
  `s_val` varchar(512) NOT NULL,
  PRIMARY KEY (`s_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO `settings` (`s_key`, `s_val`) VALUES ('threshold', '10');
INSERT IGNORE INTO `settings` (`s_key`, `s_val`) VALUES ('forecast_telegram', 'yes');

CREATE TABLE IF NOT EXISTS `wind_forecast_ai` (
  `period` datetime NOT NULL,
  `location` varchar(10) NOT NULL DEFAULT 'ky',
  `report_he` text NOT NULL,
  `report_en` text NOT NULL,
  PRIMARY KEY (`period`, `location`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `wind_home` (
  `datetime` datetime NOT NULL,
  `wind` double NOT NULL,
  `wind_sensor` double NOT NULL,
  PRIMARY KEY (`datetime`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
