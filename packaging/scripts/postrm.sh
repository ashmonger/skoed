#!/bin/sh
# postrm — clean up systemd state on full purge. /var/lib/dblock is
# only deleted on `apt purge`, never on plain `remove`.
set -eu

case "${1:-}" in
    purge)
        rm -rf /var/lib/dblock /var/log/dblock /etc/dblock
        if getent passwd dblock >/dev/null 2>&1; then
            deluser --quiet dblock >/dev/null 2>&1 || true
        fi
        if getent group dblock >/dev/null 2>&1; then
            delgroup --quiet dblock >/dev/null 2>&1 || true
        fi
        ;;
    *)
        :
        ;;
esac

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
fi
