#!/bin/bash
# Install systemd timer for daily OpenSkiron / Open-Meteo / IMS fetch at 09:00 Israel time,
# plus 17:30 for the IMS sea forecast's afternoon issue only.
set -euo pipefail

INSTALL="${1:-/opt/ikite-go}"

sudo tee /etc/systemd/system/ikite-extforecast.service >/dev/null <<UNIT
[Unit]
Description=ikite-go external forecast collector (OpenSkiron, Open-Meteo, IMS)
After=network.target mysql.service
Requires=mysql.service

[Service]
Type=oneshot
User=ikite
Group=ikite
WorkingDirectory=${INSTALL}
EnvironmentFile=/etc/ikite-go/env
ExecStart=${INSTALL}/bin/extforecast
UNIT

sudo tee /etc/systemd/system/ikite-extforecast.timer >/dev/null <<'UNIT'
[Unit]
Description=Daily external forecast at 09:00 (and the IMS sea forecast again at 17:30) Asia/Jerusalem

[Timer]
OnCalendar=*-*-* 09:00:00 Asia/Jerusalem
OnCalendar=*-*-* 17:30:00 Asia/Jerusalem
Persistent=true
Unit=ikite-extforecast.service

[Install]
WantedBy=timers.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now ikite-extforecast.timer
echo "Enabled ikite-extforecast.timer (daily 09:00, IMS sea forecast also 17:30, Asia/Jerusalem)"
