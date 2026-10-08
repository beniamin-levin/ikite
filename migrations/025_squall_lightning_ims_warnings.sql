-- Lightning (EUMETSAT MTG Lightning Imager, 5-minute frames) around Kiryat Haim,
-- one row per frame, kept for replays. threat_km is the nearest flash on the sea
-- side (or within 12 km from any side), NULL when there was none.
CREATE TABLE IF NOT EXISTS `squall_lightning` (
  `frame_at`        DATETIME     NOT NULL,
  `fetched_at`      DATETIME     NOT NULL,
  `png_file`        VARCHAR(128) NOT NULL,
  `px`              INT          NOT NULL,
  `within40_px`     INT          NOT NULL,
  `nearest_km`      FLOAT        NULL,
  `nearest_bearing` SMALLINT     NULL,
  `threat_km`       FLOAT        NULL,
  `threat_bearing`  SMALLINT     NULL,
  PRIMARY KEY (`frame_at`)
);

-- IMS official warnings that cover Haifa Bay (Zevulun valley, Carmel coast, the
-- northern sea), each sent to the storm bot once.
CREATE TABLE IF NOT EXISTS `squall_ims_warning` (
  `wid`             INT UNSIGNED NOT NULL,
  `warning_type_id` SMALLINT     NOT NULL,
  `severity_id`     SMALLINT     NOT NULL,
  `valid_from`      DATETIME     NOT NULL,
  `valid_to`        DATETIME     NOT NULL,
  `regions`         VARCHAR(255) NOT NULL,
  `text_en`         TEXT         NOT NULL,
  `text_he`         TEXT         NOT NULL,
  `sent_at`         DATETIME     NULL,
  PRIMARY KEY (`wid`)
);
