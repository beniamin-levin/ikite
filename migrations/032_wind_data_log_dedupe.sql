-- wind_data_log had no key, so INSERT IGNORE never skipped anything and each
-- IMS run re-logged its whole 3-hour window: about 10 copies of every reading
-- by 2026-10-18 (639k rows for 63k readings). Rebuild it with one row per
-- location and period, keeping the copy logged last (IMS sometimes revises a
-- reading). Rerunnable: the work table is rebuilt from scratch each time.
DROP TABLE IF EXISTS `wind_data_log_new`;
CREATE TABLE `wind_data_log_new` (
  `period`   DATETIME    NOT NULL,
  `location` VARCHAR(50) NOT NULL,
  `log`      TEXT        NOT NULL,
  PRIMARY KEY (`location`, `period`)
) ENGINE=MyISAM DEFAULT CHARSET=latin1;
INSERT INTO `wind_data_log_new` (`period`, `location`, `log`)
  SELECT `period`, `location`, `log` FROM `wind_data_log`
  ON DUPLICATE KEY UPDATE `log` = VALUES(`log`);
RENAME TABLE `wind_data_log` TO `wind_data_log_dup`, `wind_data_log_new` TO `wind_data_log`;
DROP TABLE IF EXISTS `wind_data_log_dup`;
