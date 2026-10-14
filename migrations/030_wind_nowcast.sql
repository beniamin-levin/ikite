-- ikite's hourly in-day re-issue of the estimate: today's remaining hours,
-- corrected from how far the meter has been from the morning estimate (see
-- internal/collector/estimate_nowcast.go). Same shape as wind_estimate, kept
-- apart so wind_estimate keeps the morning issue the accuracy panel scores.
CREATE TABLE IF NOT EXISTS `wind_nowcast` (
  `location`   VARCHAR(50)      NOT NULL,
  `period`     DATETIME         NOT NULL,
  `wind`       FLOAT            NOT NULL,
  `gust`       FLOAT            NOT NULL,
  `wind_low`   FLOAT            NOT NULL,
  `wind_high`  FLOAT            NOT NULL,
  `wind_dir`   SMALLINT         NULL,
  `models`     TINYINT UNSIGNED NOT NULL,
  `issued_at`  DATETIME         NOT NULL,
  PRIMARY KEY (`location`, `period`)
);
