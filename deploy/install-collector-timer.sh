#!/usr/bin/env bash
# Install/refresh the main ikite-collector timer (KY/KH + alerts).
# Polls every minute; app-level minute slots decide when to fetch.
set -euo pipefail

UNIT_DIR=/etc/systemd/system
SERVICE=ikite-collector.service
TIMER=ikite-collector.timer

sudo tee "$UNIT_DIR/$SERVICE" >/dev/null <<'EOF'
[Unit]
Description=ikite collector (KY/KH + alerts)
After=network-online.target mysql.service
Wants=network-online.target

[Service]
Type=oneshot
User=ikite
Group=ikite
EnvironmentFile=/etc/ikite-go/env
WorkingDirectory=/opt/ikite-go
ExecStart=/opt/ikite-go/bin/collector
Nice=10
EOF

sudo tee "$UNIT_DIR/$TIMER" >/dev/null <<'EOF'
[Unit]
Description=Run ikite collector every minute

[Timer]
OnBootSec=1min
OnUnitActiveSec=1min
AccuracySec=1s
Persistent=true

[Install]
WantedBy=timers.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now "$TIMER"
sudo systemctl restart "$TIMER"
systemctl list-timers "$TIMER" --no-pager
