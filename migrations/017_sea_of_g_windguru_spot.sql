DELETE FROM `wind_forecast`
WHERE `location` = '2752'
  AND `windguru_id` = 856408
  AND `id_model` < 1000000;

UPDATE `spots`
SET `windguru_id` = 729348
WHERE `id` = '2752';
