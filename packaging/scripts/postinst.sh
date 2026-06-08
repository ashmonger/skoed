#!/bin/sh
# postinst — wire up systemd. Does NOT auto-start; the operator runs
# `systemctl enable --now skoed` when ready.
set -eu

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
fi

# Fix ownership in case dpkg ran scripts before nfpm's contents stanza
# set them. Harmless on first install.
chown -R skoed:skoed /var/lib/skoed /var/log/skoed 2>/dev/null || true

cat <<'NEXT'
skoed installed.

Next steps:
  1. Edit /etc/skoed/config.yaml (default is single-node + listen on :53).
  2. Enable and start the service:
        sudo systemctl enable --now skoed
  3. Open http://<host>:8080 and set your admin password.

Docs:  https://github.com/skoed/skoed
NEXT
