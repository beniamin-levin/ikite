-- Map KY/KH to the IMS stations shown on the Haifa Bay map:
--   KY → 78 AFEQ (10-min wind)
--   KH → 41 HAIFA REFINERIES (10-min wind)
-- 318/310 are 1-min rain-only siblings and are not used for wind.

UPDATE `spots` SET `ims_station_id` = 78 WHERE `id` = 'ky';
UPDATE `spots` SET `ims_station_id` = 41 WHERE `id` = 'kh';
