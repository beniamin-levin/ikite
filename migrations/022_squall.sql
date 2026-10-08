-- Squall alerts for Kiryat Haim (internal/squall).
--
-- squall_frame: one row per RainViewer radar frame, kept forever so storms can be
-- replayed and the alert rules re-tuned. The frame image itself is archived on
-- disk (png_file, relative to the radar archive directory).
CREATE TABLE IF NOT EXISTS `squall_frame` (
  `frame_at`        DATETIME     NOT NULL,
  `fetched_at`      DATETIME     NOT NULL,
  `path`            VARCHAR(64)  NOT NULL,
  `png_file`        VARCHAR(128) NOT NULL,
  `max_dbz`         TINYINT      NOT NULL,
  `strong_px`       INT          NOT NULL,
  `cells`           SMALLINT     NOT NULL,
  `motion_ok`       TINYINT(1)   NOT NULL,
  `speed_kmh`       FLOAT        NULL,
  `heading_deg`     SMALLINT     NULL,
  `nearest_km`      FLOAT        NULL,
  `nearest_bearing` SMALLINT     NULL,
  `threat_eta_min`  FLOAT        NULL,
  `threat_pass_km`  FLOAT        NULL,
  `threat_dbz`      TINYINT      NULL,
  PRIMARY KEY (`frame_at`)
);

-- squall_alert: every Telegram message the squall service sent.
CREATE TABLE IF NOT EXISTS `squall_alert` (
  `id`       INT UNSIGNED NOT NULL AUTO_INCREMENT,
  `kind`     VARCHAR(16)  NOT NULL, -- radar | station | report | review
  `sent_at`  DATETIME     NOT NULL,
  `frame_at` DATETIME     NULL,
  `eta_min`  FLOAT        NULL,
  `message`  TEXT         NOT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_squall_alert_kind_sent` (`kind`, `sent_at`)
);

-- squall_event: a squall measured at the spot, and what replaying the radar
-- against it showed.
CREATE TABLE IF NOT EXISTS `squall_event` (
  `id`               INT UNSIGNED NOT NULL AUTO_INCREMENT,
  `location`         VARCHAR(20)  NOT NULL,
  `start_at`         DATETIME     NOT NULL,
  `end_at`           DATETIME     NULL,
  `peak_wind`        FLOAT        NOT NULL,
  `peak_gust`        FLOAT        NOT NULL,
  `peak_at`          DATETIME     NOT NULL,
  `dir_deg`          SMALLINT     NULL,
  `baseline`         FLOAT        NOT NULL,
  `kind`             VARCHAR(8)   NULL,  -- storm (radar saw it) | dry (no radar cell)
  `alert_lead_min`   FLOAT        NULL,  -- warning actually given
  `replay_lead_min`  FLOAT        NULL,  -- warning the tuned rules would give
  `obs_lead_km`      FLOAT        NULL,  -- rain core distance when the wind hit
  `obs_speed_factor` FLOAT        NULL,  -- projected / actual arrival time
  `analyzed_at`      DATETIME     NULL,
  `report`           TEXT         NULL,
  `reviewed_at`      DATETIME     NULL,  -- set by the follow-up review
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_squall_event` (`location`, `start_at`)
);

-- squall_tuning: every re-fit of the alert rules, with the before/after score.
CREATE TABLE IF NOT EXISTS `squall_tuning` (
  `id`            INT UNSIGNED NOT NULL AUTO_INCREMENT,
  `tuned_at`      DATETIME     NOT NULL,
  `event_id`      INT UNSIGNED NULL,
  `params_before` TEXT         NOT NULL,
  `params_after`  TEXT         NOT NULL,
  `score_before`  FLOAT        NOT NULL,
  `score_after`   FLOAT        NOT NULL,
  `notes`         TEXT         NOT NULL,
  PRIMARY KEY (`id`)
);

-- squall_ims_frame: the IMS rain radar (5-minute frames), archived for analysis.
CREATE TABLE IF NOT EXISTS `squall_ims_frame` (
  `frame_at`   DATETIME     NOT NULL,
  `fetched_at` DATETIME     NOT NULL,
  `source`     VARCHAR(160) NOT NULL,
  `png_file`   VARCHAR(128) NOT NULL,
  `bytes`      INT          NOT NULL,
  PRIMARY KEY (`frame_at`)
);
