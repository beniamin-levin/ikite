#!/bin/bash
# Install systemd timer for IMS Envista observations every 10 minutes.
set -euo pipefail

INSTALL="${1:-/opt/ikite-go}"

sudo tee /etc/systemd/system/ikite-ims.service >/dev/null <<UNIT
[Unit]
Description=ikite-go IMS Envista observation collector
After=network.target mysql.service
Requires=mysql.service

[Service]
Type=oneshot
User=ikite
Group=ikite
WorkingDirectory=${INSTALL}
EnvironmentFile=/etc/ikite-go/env
ExecStart=${INSTALL}/bin/ims
UNIT

sudo tee /etc/systemd/system/ikite-ims.timer >/dev/null <<'UNIT'
[Unit]
Description=IMS observations every 10 minutes

[Timer]
OnBootSec=2min
OnUnitActiveSec=10min
Persistent=true
Unit=ikite-ims.service

[Install]
WantedBy=timers.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now ikite-ims.timer
echo "Enabled ikite-ims.timer (every 10 minutes after last run)"
