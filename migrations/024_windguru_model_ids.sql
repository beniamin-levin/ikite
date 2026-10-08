-- Windguru model ids, corrected against Windguru's own model list (2026-10-06).
-- WRF 3 km is model 90 and WRF 9 km is model 23 - they had been stored as 23
-- and 42. Move 23 to 90 first so the two never collide.
UPDATE wind_forecast SET id_model = 90 WHERE id_model = 23 AND model = 'wrf3';
UPDATE wind_forecast SET id_model = 23 WHERE id_model = 42 AND model = 'wrf9';

-- An official Windguru spot nearby to borrow high-resolution models from (WRF* 1 km,
-- Zephr-HD 2.6 km, WRF 3 and 9 km, ICON 7 km), which Windguru keeps for PRO users
-- on custom spots. 378048 is the official Kiryat Yam spot, at the same point as
-- the custom spot 373090 and 2.5 km from Kiryat Haim.
ALTER TABLE spots ADD COLUMN windguru_hires_id INT NULL;
UPDATE spots SET windguru_hires_id = 378048 WHERE id IN ('ky', 'kh');
