#!/bin/bash
# Install systemd timer for the openWRF forecast fetch.
# Hourly across the day: the publisher's upload time drifts, and a single daily
# attempt loses the whole day when the file lands late. The collector skips spots
# that already have today's rows, so repeat runs are cheap.
set -euo pipefail

INSTALL="${1:-/opt/ikite-go}"

sudo tee /etc/systemd/system/ikite-openwrf.service >/dev/null <<UNIT
[Unit]
Description=ikite-go openWRF forecast collector
After=network.target mysql.service
Requires=mysql.service

[Service]
Type=oneshot
User=ikite
Group=ikite
WorkingDirectory=${INSTALL}
EnvironmentFile=/etc/ikite-go/env
ExecStart=${INSTALL}/bin/openwrf
UNIT

sudo tee /etc/systemd/system/ikite-openwrf.timer >/dev/null <<'UNIT'
[Unit]
Description=openWRF forecast fetch, hourly 05:00-21:00 Asia/Jerusalem

[Timer]
OnCalendar=*-*-* 05..21:00:00 Asia/Jerusalem
Persistent=true
Unit=ikite-openwrf.service

[Install]
WantedBy=timers.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now ikite-openwrf.timer
echo "Enabled ikite-openwrf.timer (hourly 05:00-21:00 Asia/Jerusalem)"
