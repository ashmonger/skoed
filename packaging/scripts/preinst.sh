#!/bin/sh
# preinst — create the skoed system user/group if missing.
set -eu

if ! getent group skoed >/dev/null 2>&1; then
    addgroup --system skoed
fi
if ! getent passwd skoed >/dev/null 2>&1; then
    adduser --system --ingroup skoed --home /var/lib/skoed \
            --no-create-home --shell /usr/sbin/nologin \
            --gecos "skoed service" skoed
fi
