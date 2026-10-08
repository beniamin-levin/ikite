#!/bin/bash
# Install systemd timer for ikite's blended best-estimate wind.
# Runs 30 minutes after each forecast source lands — Windguru at 07:00 and
# Open-Meteo at 09:00 — so the estimate always reflects the newest models.
set -euo pipefail

INSTALL="${1:-/opt/ikite-go}"

sudo tee /etc/systemd/system/ikite-estimate.service >/dev/null <<UNIT
[Unit]
Description=ikite-go blended wind estimate (calibrated against spot meters)
After=network.target mysql.service
# Wants, not Requires: a Requires= dependency is stopped whenever mysql is, which
# is what took ikite-server down during a mysql package upgrade on 2026-09-01.
Wants=mysql.service

[Service]
Type=oneshot
User=ikite
Group=ikite
WorkingDirectory=${INSTALL}
EnvironmentFile=/etc/ikite-go/env
ExecStart=${INSTALL}/bin/estimate
UNIT

sudo tee /etc/systemd/system/ikite-estimate.timer >/dev/null <<'UNIT'
[Unit]
Description=Blended wind estimate after each forecast source (07:30, 09:30 Asia/Jerusalem)

[Timer]
OnCalendar=*-*-* 07:30:00 Asia/Jerusalem
OnCalendar=*-*-* 09:30:00 Asia/Jerusalem
Persistent=true
Unit=ikite-estimate.service

[Install]
WantedBy=timers.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now ikite-estimate.timer
echo "Enabled ikite-estimate.timer (daily 07:30 and 09:30 Asia/Jerusalem)"
