-- Past model forecasts from the Windguru archive (fetched once, from Ben's own
-- browser session), kept apart from the forecasts ikite saves itself. The
-- estimate's calibration can use them where ikite has no saved forecast.

CREATE TABLE IF NOT EXISTS `wind_forecast_archive` (
  `location`   VARCHAR(32)  NOT NULL,
  `model`      VARCHAR(32)  NOT NULL,
  `period`     DATETIME     NOT NULL,
  `wind`       FLOAT        NOT NULL,
  `gust`       FLOAT        NULL,
  `wind_dir`   SMALLINT     NULL,
  `wg_spot`    INT UNSIGNED NOT NULL,
  `fetched_at` DATETIME     NOT NULL,
  PRIMARY KEY (`location`, `model`, `period`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
