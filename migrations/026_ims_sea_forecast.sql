-- The IMS official sea forecast, every block of every issue, kept in full:
-- wind direction and speed ranges, sea state, wave height and sea temperature.
-- Its wind also goes into wind_forecast as the ims_sea model (id 1000006).
CREATE TABLE IF NOT EXISTS `ims_sea_forecast` (
  `issued_at`    DATETIME     NOT NULL,
  `region_id`    SMALLINT     NOT NULL,
  `region_name`  VARCHAR(40)  NOT NULL,
  `valid_from`   DATETIME     NOT NULL,
  `valid_to`     DATETIME     NOT NULL,
  `dir_from`     SMALLINT     NULL,
  `dir_to`       SMALLINT     NULL,
  `wind_min_kmh` FLOAT        NULL,
  `wind_max_kmh` FLOAT        NULL,
  `sea_state`    SMALLINT     NULL,
  `wave_min_cm`  SMALLINT     NULL,
  `wave_max_cm`  SMALLINT     NULL,
  `sea_temp_c`   FLOAT        NULL,
  `fetched_at`   DATETIME     NOT NULL,
  PRIMARY KEY (`issued_at`, `region_id`, `valid_from`)
);
