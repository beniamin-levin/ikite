#!/bin/bash
# Install systemd timer for ikite's hourly nowcast: from 09:35 to 18:35 the
# rest of today's estimate is nudged towards what each meter has measured so
# far (see internal/collector/estimate_nowcast.go).
set -euo pipefail

INSTALL="${1:-/opt/ikite-go}"

sudo tee /etc/systemd/system/ikite-nowcast.service >/dev/null <<UNIT
[Unit]
Description=ikite-go hourly nowcast (today's estimate corrected from the meters)
After=network.target mysql.service
Wants=mysql.service

[Service]
Type=oneshot
User=ikite
Group=ikite
WorkingDirectory=${INSTALL}
EnvironmentFile=/etc/ikite-go/env
ExecStart=${INSTALL}/bin/estimate -nowcast
UNIT

sudo tee /etc/systemd/system/ikite-nowcast.timer >/dev/null <<'UNIT'
[Unit]
Description=Hourly nowcast (09:35–18:35 Asia/Jerusalem)

[Timer]
OnCalendar=*-*-* 09..18:35:00 Asia/Jerusalem
Unit=ikite-nowcast.service

[Install]
WantedBy=timers.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now ikite-nowcast.timer
echo "Enabled ikite-nowcast.timer (hourly 09:35–18:35 Asia/Jerusalem)"
