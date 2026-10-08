-- Betzet's history: the legacy site stored Windguru station 1011 (the old
-- Betzet meter) as location bt from Feb 2023. It reported only zeros from
-- 2025-06-01 until station 15233 replaced it on 2025-10-12. Same fields
-- (Windguru wind_min / wind_max), so the valid bt rows become Betzet (15233).
-- The dead all-zero rows stay under bt. bz (Diamond, May 2023) is untouched.

UPDATE `wind_data` SET `location` = '15233'
WHERE `location` = 'bt' AND `period` <= '2025-06-01 01:00:02';
