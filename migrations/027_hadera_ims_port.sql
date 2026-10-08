-- Link Hadera to IMS station 46 HADERA PORT (same coordinates as the spot), so
-- its forecast page shows the IMS readings and the live table gets an
-- IMS Hadera Port column, as KY/KH have Afeq/Refineries.

UPDATE `spots` SET `ims_station_id` = 46 WHERE `id` = 'hp';
