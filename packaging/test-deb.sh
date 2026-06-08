#!/usr/bin/env bash
# test-deb.sh — smoke-test the .deb in a clean debian:bookworm container.
# Runs locally + in CI. Requires docker.
#
# Pass the .deb path as the first arg, or the script picks the
# newest dist/dblock_*_amd64.deb.

set -euo pipefail
cd "$(dirname "$0")/.."

PKG="${1:-$(ls -t dist/dblock_*_amd64.deb 2>/dev/null | head -1 || true)}"
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
    -v "$PWD/$PKG:/tmp/dblock.deb:ro" \
    debian:bookworm bash <<'INNER'
set -euo pipefail
echo "--- dpkg-deb --info"
dpkg-deb --info /tmp/dblock.deb

echo "--- dpkg-deb --contents (filtered)"
dpkg-deb --contents /tmp/dblock.deb | grep -E '/usr/bin/dblock|/lib/systemd/system/dblock|/etc/dblock|/var/lib/dblock'

echo "--- apt-get install (resolves adduser dep)"
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends adduser

echo "--- dpkg -i /tmp/dblock.deb"
dpkg -i /tmp/dblock.deb

echo "--- binary smoke test (--help)"
/usr/bin/dblock --help 2>&1 | head -3 || true
# --version isn't yet wired; just confirm the binary is executable.
test -x /usr/bin/dblock

echo "--- systemd unit syntax"
test -r /lib/systemd/system/dblock.service
systemd-analyze verify /lib/systemd/system/dblock.service 2>&1 || true

echo "--- config conffile present"
test -r /etc/dblock/config.yaml

echo "--- dblock user/group created"
id dblock
INNER

echo "==> OK"
