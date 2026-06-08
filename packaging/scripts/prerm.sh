#!/bin/sh
# prerm — stop the service before removal. Leaves /var/lib/dblock in
# place so an `apt remove` + re-install keeps existing cluster state.
set -eu

if [ -d /run/systemd/system ]; then
    if systemctl is-active --quiet dblock 2>/dev/null; then
        systemctl stop dblock || true
    fi
    if systemctl is-enabled --quiet dblock 2>/dev/null; then
        systemctl disable dblock || true
    fi
fi
