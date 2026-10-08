#!/bin/bash
# Install systemd timer for daily 25+ kt forecast gust Telegram alert at 08:10 Israel time.
# Runs after Windguru (07:00) and openWRF (08:00) collectors have finished.
set -euo pipefail

INSTALL="${1:-/opt/ikite-go}"

sudo tee /etc/systemd/system/ikite-forecastgust.service >/dev/null <<UNIT
[Unit]
Description=ikite-go forecast gust 25+ Telegram notify
After=network.target mysql.service
Requires=mysql.service

[Service]
Type=oneshot
User=ikite
Group=ikite
WorkingDirectory=${INSTALL}
EnvironmentFile=/etc/ikite-go/env
ExecStart=${INSTALL}/bin/forecastgust
UNIT

sudo tee /etc/systemd/system/ikite-forecastgust.timer >/dev/null <<'UNIT'
[Unit]
Description=Daily forecast gust alert at 08:10 Asia/Jerusalem

[Timer]
OnCalendar=*-*-* 08:10:00 Asia/Jerusalem
Persistent=true
Unit=ikite-forecastgust.service

[Install]
WantedBy=timers.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now ikite-forecastgust.timer
echo "Enabled ikite-forecastgust.timer (daily 08:10 Asia/Jerusalem)"
systemctl list-timers --all | grep -E 'ikite-forecastgust|ikite-wgforecast|ikite-openwrf' || true
