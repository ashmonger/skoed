#!/bin/sh
# prerm — stop the service before removal. Leaves /var/lib/skoed in
# place so an `apt remove` + re-install keeps existing cluster state.
set -eu

if [ -d /run/systemd/system ]; then
    if systemctl is-active --quiet skoed 2>/dev/null; then
        systemctl stop skoed || true
    fi
    if systemctl is-enabled --quiet skoed 2>/dev/null; then
        systemctl disable skoed || true
    fi
fi
