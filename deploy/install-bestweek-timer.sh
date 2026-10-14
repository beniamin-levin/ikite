#!/bin/bash
# Install systemd timer for the morning "best kite days" Telegram summary,
# 15 minutes after the 07:30 estimate.
set -euo pipefail

INSTALL="${1:-/opt/ikite-go}"

sudo tee /etc/systemd/system/ikite-bestweek.service >/dev/null <<UNIT
[Unit]
Description=ikite-go best kite days summary to Telegram
After=network.target mysql.service
Wants=mysql.service

[Service]
Type=oneshot
User=ikite
Group=ikite
WorkingDirectory=${INSTALL}
EnvironmentFile=/etc/ikite-go/env
ExecStart=${INSTALL}/bin/bestweek
UNIT

sudo tee /etc/systemd/system/ikite-bestweek.timer >/dev/null <<'UNIT'
[Unit]
Description=Best kite days summary (07:45 Asia/Jerusalem)

[Timer]
OnCalendar=*-*-* 07:45:00 Asia/Jerusalem
Unit=ikite-bestweek.service

[Install]
WantedBy=timers.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now ikite-bestweek.timer
echo "Enabled ikite-bestweek.timer (daily 07:45 Asia/Jerusalem)"
