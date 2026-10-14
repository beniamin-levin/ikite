-- Hadera (hp) is the spot kiters call Sdot Yam. Its meter stays IMS Hadera
-- Port (Windguru 3377).
UPDATE `spots` SET `name` = 'Sdot Yam' WHERE `id` = 'hp';

-- Second meter for Sdot Yam: Windguru 2259, Freegull Sea Sports on the Sdot
-- Yam beach. It is collected on hp's schedule and shown in hp's cells (lower
-- wind, higher gust of the two), see models.SecondaryMeters.
INSERT IGNORE INTO `spots`
  (`id`, `name`, `windguru_station_id`, `sort_order`, `visible`, `collect`,
   `collect_interval_min`, `collect_start_hour`, `collect_end_hour`)
VALUES
  ('2259', 'Sdot Yam (Freegull)', 2259, 71, 0, 1, 5, 8, 22);

-- Beit Yanai: forecast-only spot (no meter) for /best. Windguru custom spot
-- 593931 on the beach, coordinates for the Open-Meteo and openWRF models. Its
-- estimate is calibrated on Hadera Port (collector.calibrationMeter).
INSERT IGNORE INTO `spots`
  (`id`, `name`, `windguru_id`, `lat`, `lon`, `sort_order`, `visible`, `collect`,
   `collect_interval_min`, `collect_start_hour`, `collect_end_hour`)
VALUES
  ('by', 'Beit Yanai', 593931, 32.380, 34.862, 75, 0, 1, 5, 8, 22);
