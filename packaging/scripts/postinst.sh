#!/bin/sh
# postinst — wire up systemd. Does NOT auto-start; the operator runs
# `systemctl enable --now dblock` when ready.
set -eu

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
fi

# Fix ownership in case dpkg ran scripts before nfpm's contents stanza
# set them. Harmless on first install.
chown -R dblock:dblock /var/lib/dblock /var/log/dblock 2>/dev/null || true

cat <<'NEXT'
dblock installed.

Next steps:
  1. Edit /etc/dblock/config.yaml (default is single-node + listen on :53).
  2. Enable and start the service:
        sudo systemctl enable --now dblock
  3. Open http://<host>:8080 and set your admin password.

Docs:  https://github.com/dblock/dblock
NEXT
