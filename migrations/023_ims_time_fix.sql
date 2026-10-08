-- IMS observations collected before 2026-10-06 15:00 were stored an hour early:
-- the parser trusted the +03:00 offset IMS appends to its (standard-time)
-- summer timestamps. See parseIMSTime in internal/sources/ims. All of those rows
-- are from summer time (IMS collection began 2026-08-06, DST ends 2026-10-25),
-- so each moves forward exactly one hour. Newest first, so no row collides with
-- one not yet moved. Rows from the fixed collector are after the cutoff.
-- (No semicolons in these comments: the migration runner splits on them.)
UPDATE wind_data SET period = period + INTERVAL 1 HOUR
  WHERE location LIKE 'ims%' AND period < '2026-10-06 15:00:00'
  ORDER BY period DESC;
UPDATE wind_data_log SET period = period + INTERVAL 1 HOUR
  WHERE location LIKE 'ims%' AND period < '2026-10-06 15:00:00'
  ORDER BY period DESC;
