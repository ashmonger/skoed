#!/usr/bin/env bash
# test-deb.sh — smoke-test the .deb in a clean debian:bookworm container.
# Runs locally + in CI. Requires docker.
#
# Pass the .deb path as the first arg, or the script picks the
# newest dist/skoed_*_amd64.deb.

set -euo pipefail
cd "$(dirname "$0")/.."

PKG="${1:-$(ls -t dist/skoed_*_amd64.deb 2>/dev/null | head -1 || true)}"
if [ -z "${PKG:-}" ] || [ ! -r "$PKG" ]; then
    echo "no .deb found — run 'make deb' first" >&2
    exit 2
fi

echo "==> testing $PKG"

# Run a fresh Debian bookworm container, copy the deb in, install,
# inspect the unit. Skipping --privileged: we only assert install
# correctness, not service start (that would need systemd in the
# container).
docker run --rm -i \
    -v "$PWD/$PKG:/tmp/skoed.deb:ro" \
    debian:bookworm bash <<'INNER'
set -euo pipefail
echo "--- dpkg-deb --info"
dpkg-deb --info /tmp/skoed.deb

echo "--- dpkg-deb --contents (filtered)"
dpkg-deb --contents /tmp/skoed.deb | grep -E '/var/lib/skoed/bin/skoed|/lib/systemd/system/skoed|/etc/skoed|/var/lib/skoed'

echo "--- apt-get install (resolves adduser dep)"
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends adduser

echo "--- dpkg -i /tmp/skoed.deb"
dpkg -i /tmp/skoed.deb

echo "--- binary smoke test (--help)"
/var/lib/skoed/bin/skoed --help 2>&1 | head -3 || true
# --version isn't yet wired; just confirm the binary is executable.
test -x /var/lib/skoed/bin/skoed

echo "--- systemd unit syntax"
test -r /lib/systemd/system/skoed.service
systemd-analyze verify /lib/systemd/system/skoed.service 2>&1 || true

echo "--- config conffile present"
test -r /etc/skoed/config.yaml

echo "--- skoed user/group created"
id skoed
INNER

echo "==> OK"
