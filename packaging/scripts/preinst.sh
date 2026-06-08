#!/bin/sh
# preinst — create the dblock system user/group if missing.
set -eu

if ! getent group dblock >/dev/null 2>&1; then
    addgroup --system dblock
fi
if ! getent passwd dblock >/dev/null 2>&1; then
    adduser --system --ingroup dblock --home /var/lib/dblock \
            --no-create-home --shell /usr/sbin/nologin \
            --gecos "dblock service" dblock
fi
