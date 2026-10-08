-- Prefer active IMS stations (Haifa Port 26 is inactive / stale).
-- 41 = HAIFA REFINERIES, 343 = SHAVE ZIYYON.
UPDATE `spots` SET `ims_station_id` = 41 WHERE `id` IN ('kh', 'bg', 'ky');
UPDATE `spots` SET `ims_station_id` = 343 WHERE `id` = 'st';
