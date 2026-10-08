-- ikite's own best-estimate wind, blended from the model forecasts and
-- calibrated against each spot's meter (see internal/estimate). One row per spot
-- per hour. Each run replaces today-and-forward; past days keep the estimate
-- issued that day, so the estimate builds a track record the accuracy panel can
-- score, the same way it scores the models.
CREATE TABLE IF NOT EXISTS `wind_estimate` (
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
