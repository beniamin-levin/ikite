#!/bin/bash
# Install systemd timer for daily openWRF forecast fetch at 08:00 Israel time.
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
Description=Daily openWRF forecast at 08:00 Asia/Jerusalem

[Timer]
OnCalendar=*-*-* 08:00:00 Asia/Jerusalem
Persistent=true
Unit=ikite-openwrf.service

[Install]
WantedBy=timers.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now ikite-openwrf.timer
echo "Enabled ikite-openwrf.timer (daily 08:00 Asia/Jerusalem)"
