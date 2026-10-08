-- Coordinates for point-forecast APIs (Open-Meteo, OpenSkiron) and optional IMS station mapping.
ALTER TABLE `spots`
  ADD COLUMN `lat` double DEFAULT NULL AFTER `windguru_id`,
  ADD COLUMN `lon` double DEFAULT NULL AFTER `lat`,
  ADD COLUMN `ims_station_id` int DEFAULT NULL AFTER `lon`;

UPDATE `spots` SET `lat` = 32.8512, `lon` = 35.0616, `ims_station_id` = 41 WHERE `id` = 'ky';   -- Haifa Refineries
UPDATE `spots` SET `lat` = 32.8300, `lon` = 35.0700, `ims_station_id` = 26 WHERE `id` = 'kh';   -- Haifa Port
UPDATE `spots` SET `lat` = 32.8320, `lon` = 34.9720, `ims_station_id` = 26 WHERE `id` = 'bg';   -- Haifa Port
UPDATE `spots` SET `lat` = 33.0780, `lon` = 35.1100, `ims_station_id` = 239 WHERE `id` = '15233'; -- Elon
UPDATE `spots` SET `lat` = 32.9800, `lon` = 35.0830, `ims_station_id` = 42 WHERE `id` = 'st';  -- Haifa University
UPDATE `spots` SET `lat` = 32.8700, `lon` = 35.5750, `ims_station_id` = 233 WHERE `id` = '2752'; -- Kefar Nahum
UPDATE `spots` SET `lat` = 32.8800, `lon` = 35.5800, `ims_station_id` = 233 WHERE `id` = '1909'; -- Diamond / Kefar Nahum
UPDATE `spots` SET `lat` = 32.7000, `lon` = 35.5900, `ims_station_id` = 2 WHERE `id` = '5731';   -- Avne Etan
UPDATE `spots` SET `lat` = 32.8200, `lon` = 35.7600, `ims_station_id` = 2 WHERE `id` = '5732';   -- Avne Etan
UPDATE `spots` SET `lat` = 33.0000, `lon` = 35.7700, `ims_station_id` = 10 WHERE `id` = '5730';  -- Merom Golan
