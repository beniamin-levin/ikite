#!/bin/bash
# Install the squall alert timer: every minute, check new radar frames
# (RainViewer, IMS), the upwind stations and the Kiryat Haim meter, send storm
# alerts to the storm Telegram bot, and re-tune after each storm.
# Needs TELEGRAM_STORM_TOKEN and TELEGRAM_STORM_CHAT_ID in /etc/ikite-go/env.
set -euo pipefail

INSTALL="${1:-/opt/ikite-go}"
ARCHIVE="${2:-/var/lib/ikite-go/radar}"

sudo install -d -o ikite -g ikite -m 755 "${ARCHIVE}"

sudo tee /etc/systemd/system/ikite-squall.service >/dev/null <<UNIT
[Unit]
Description=ikite-go squall alerts (radar + upwind stations) for Kiryat Haim
After=network.target mysql.service
# Wants, not Requires: see install-estimate-timer.sh.
Wants=mysql.service

[Service]
Type=oneshot
User=ikite
Group=ikite
WorkingDirectory=${INSTALL}
EnvironmentFile=/etc/ikite-go/env
Environment=SQUALL_ARCHIVE_DIR=${ARCHIVE}
ExecStart=${INSTALL}/bin/squall
# A post-storm replay can take a while; never let one run pile onto the next.
TimeoutStartSec=15min
UNIT

sudo tee /etc/systemd/system/ikite-squall.timer >/dev/null <<'UNIT'
[Unit]
Description=Squall alerts every minute

[Timer]
OnBootSec=1min
OnUnitActiveSec=1min
AccuracySec=5s
Unit=ikite-squall.service

[Install]
WantedBy=timers.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now ikite-squall.timer
echo "Enabled ikite-squall.timer (every minute); radar archive in ${ARCHIVE}"
