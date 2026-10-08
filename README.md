# ikite-go

Israeli Mediterranean wind monitoring for kitesurfing/windsurfing.

## Features

- **Live wind table** (`/`) — multi-station readings with color-coded speeds and direction arrows
- **Graph** (`/graph`) — ApexCharts time series
- **Settings** (`/settings?pass=<uuid>`) — hidden admin page (threshold, spots, collectors)
- **Beach cameras** (`/camera`)
- **Home sensor ingest** (`/home?w=<knots>`)
- **Collector** — polls Windguru stations, Kiryat Yam history, Kiryat Haim (windometer), sends Telegram alerts
- **Forecast job** — fetches Hebrew AI report from surfo, translates to English, stores + Telegram
- **Windguru forecast job** — daily at 07:00, fetches all models for spots with `windguru_id` via Beget proxy
- **openWRF forecast job** — daily at 08:00, parses Kiryat Yam / matched-spot 1 km PDFs from Google Drive (multi-day, overrides overlapping hours next day)
- **External forecast job** — daily at 09:00: OpenSkiron Israel 4 km GRIB, Open-Meteo (AIFS + UKMO), and IMS station observations (36h backfill)
- **IMS observation job** — every 10 minutes: incremental IMS Envista station readings (~3h window)
- **Forecast page** (`/forecast`) — all stored models for spots checked visible in Settings

## Quick start

```bash
cp .env.example .env
# Edit .env — set DB_PASSWORD, proxy URLs, SETTINGS_PASS, Telegram tokens

docker compose up -d          # MySQL (dev credentials in compose file only)
make build
MIGRATE=1 ./bin/server        # http://localhost:8080
./bin/collector               # run every ~5 min via cron
./bin/forecast                # run every ~15–30 min via cron
./bin/wgforecast              # daily 07:00 — Windguru model forecasts (see deploy/install-wg-forecast-timer.sh)
./bin/openwrf                 # daily 08:00 — openWRF Kiryat Yam forecast (see deploy/install-openwrf-forecast-timer.sh)
./bin/forecastgust            # daily 08:10 — single 25+ kt forecast gust Telegram alert (see deploy/install-forecast-gust-timer.sh)
./bin/extforecast             # daily 09:00 — OpenSkiron + Open-Meteo + IMS backfill (see deploy/install-ext-forecast-timer.sh)
./bin/ims                     # every 10 min — IMS observations (see deploy/install-ims-timer.sh)
```

## Cron examples

```cron
*/5 * * * *  cd /path/to/ikite-go && ./bin/collector >> /var/log/ikite-collector.log 2>&1
*/20 * * * * cd /path/to/ikite-go && ./bin/forecast  >> /var/log/ikite-forecast.log 2>&1
```

Per-station Windguru timers (production): see `deploy/setup-wg-timers.sh` and `deploy/add-wg-timer.sh`.

## Config

**All secrets and deployment-specific URLs must come from environment.** See `.env.example` (local) and `deploy/env.example` (production server).

| Variable | Purpose |
|----------|---------|
| `DB_*` / `DB_DSN` | MySQL connection |
| `SETTINGS_PASS` | UUID for `/settings?pass=…` (required to access admin) |
| `TELEGRAM_ALERT_TOKEN` / `TELEGRAM_ALERT_CHAT_ID` | Wind alert bot |
| `TELEGRAM_AI_TOKEN` / `TELEGRAM_AI_CHAT_ID` | Forecast bot |
| `BEGET_PROXY_URL` | Generic Beget `proxy_post.php` — all outbound fetches POST JSON here |
| `BEGET_PROXY_SECRET` | Shared secret — must match `PROXY_SECRET` in uploaded `proxy_post.php` |
| `KY_HISTORY_URL` | Upstream Surfo KY wind JSON (`api_wind.php`) |
| `SURFO_LIVE_URL` | Upstream Surfo AI forecast JSON |
| `OPENWRF_DRIVE_URL` | Google Drive root folder URL for openWRF PDFs (first subfolder is used) |
| `OPENWRF_PDF_URL` | Optional override for a single openWRF PDF URL (testing) |
| `IMS_API_TOKEN` | Optional IMS Envista API token for IMS real columns on `/forecast` and the `ims` collector |
| `WG_TIMER_QUEUE_DIR` | Directory for pending timer requests (web writes, root cron processes) |
| `WG_TIMER_SCRIPT` | Path to `deploy/add-wg-timer.sh` (used by queue processor) |

## Layout

```
cmd/server      HTTP dashboard
cmd/collector   wind poll + alerts
cmd/wgforecast  Windguru forecast job (daily)
cmd/openwrf     openWRF forecast job (daily)
cmd/extforecast OpenSkiron + Open-Meteo + IMS backfill (daily)
cmd/ims         IMS Envista observation collector (every 10 min)
internal/       packages (store, sources, notify, web)
migrations/     MySQL schema
deploy/         systemd timers, PHP proxies, env template
```

Spots (names, Windguru IDs, visibility) are stored in the `spots` database table.

## Publishing / security

- `.env`, `server-keys/`, `*.pem`, and `bin/` are gitignored
- Never commit Telegram tokens, DB passwords, `SETTINGS_PASS`, or SSH keys
- Copy `deploy/beget/proxy_post.php.example` to `proxy_post.php`, set `PROXY_SECRET`, upload to Beget; set the same value as `BEGET_PROXY_SECRET` in env

## Not ported (yet)

Windguru global spot-rating crawler (`wg_*.php`, `spots_*.php`) — secondary subsystem from the PHP site.
