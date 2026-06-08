#!/bin/sh
# postrm — clean up systemd state on full purge. /var/lib/skoed is
# only deleted on `apt purge`, never on plain `remove`.
set -eu

case "${1:-}" in
    purge)
        rm -rf /var/lib/skoed /var/log/skoed /etc/skoed
        if getent passwd skoed >/dev/null 2>&1; then
            deluser --quiet skoed >/dev/null 2>&1 || true
        fi
        if getent group skoed >/dev/null 2>&1; then
            delgroup --quiet skoed >/dev/null 2>&1 || true
        fi
        ;;
    *)
        :
        ;;
esac

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
fi
