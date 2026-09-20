#!/usr/bin/env bash
set -euo pipefail
repo="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
export S_UI_RETRY_ROOT="$work/root" TEST_LOG="$work/systemctl.log"
mkdir -p "$work/bin" "$work/package/s-ui" "$S_UI_RETRY_ROOT/usr/local/s-ui/db" "$S_UI_RETRY_ROOT/etc/systemd/system" "$S_UI_RETRY_ROOT/usr/bin"
printf 'original binary\n' > "$S_UI_RETRY_ROOT/usr/local/s-ui/sui"
printf 'original database\n' > "$S_UI_RETRY_ROOT/usr/local/s-ui/db/s-ui.db"
printf 'original unit\n' > "$S_UI_RETRY_ROOT/etc/systemd/system/s-ui.service"
printf 'original manager\n' > "$S_UI_RETRY_ROOT/usr/bin/s-ui"
find "$S_UI_RETRY_ROOT" -type f -exec sha256sum {} \; > "$work/before.txt"
cat > "$work/bin/systemctl" <<'MOCK'
#!/bin/bash
printf '%s\n' "$*" >> "$TEST_LOG"
for arg in "$@"; do
  case "$arg" in s-ui|s-ui.service|s-ui-agent|s-ui-agent.service) echo 'Touched original service!' >&2; exit 90 ;; esac
done
if [[ "${TEST_FAIL_RESTART:-}" == true && "$1" == restart ]]; then exit 1; fi
exit 0
MOCK
printf '#!/bin/bash\nexit 0\n' > "$work/bin/ip"
printf '#!/bin/bash\nexit 0\n' > "$work/bin/sleep"
chmod +x "$work/bin/"*
export PATH="$work/bin:$PATH"
cp "$repo/retry-manage.sh" "$repo/s-ui-ipv6-retry.service" "$work/package/s-ui/"
printf '#!/bin/bash\necho independent-tool\n' > "$work/package/s-ui/sui"
tar -czf "$work/package.tar.gz" -C "$work/package" s-ui
bash "$repo/paired-release/install-s-ui-paired.sh" "$work/package.tar.gz"
sha256sum -c "$work/before.txt"
test -x "$S_UI_RETRY_ROOT/usr/local/s-ui-ipv6-retry/sui"
test -x "$S_UI_RETRY_ROOT/usr/bin/s-ui-ipv6-retry"
grep -F '/s-ui-ipv6-retry/sui' "$S_UI_RETRY_ROOT/etc/systemd/system/s-ui-ipv6-retry.service"
test ! -f "$S_UI_RETRY_ROOT/usr/local/s-ui-ipv6-retry/db/s-ui.db"
"$S_UI_RETRY_ROOT/usr/bin/s-ui-ipv6-retry"
printf 'preserve independent DB\n' > "$S_UI_RETRY_ROOT/usr/local/s-ui-ipv6-retry/db/s-ui-ipv6-retry.db"
printf '#!/bin/bash\necho new-binary\n' > "$work/package/s-ui/sui"
tar -czf "$work/update.tar.gz" -C "$work/package" s-ui
export TEST_FAIL_RESTART=true
if bash "$repo/paired-release/install-s-ui-paired.sh" "$work/update.tar.gz"; then echo 'Expected restart failure' >&2; exit 1; fi
grep -F independent-tool "$S_UI_RETRY_ROOT/usr/local/s-ui-ipv6-retry/sui"
grep -F 'preserve independent DB' "$S_UI_RETRY_ROOT/usr/local/s-ui-ipv6-retry/db/s-ui-ipv6-retry.db"
sha256sum -c "$work/before.txt"
unset TEST_FAIL_RESTART
mkdir -p "$work/legacy/s-ui"
printf 'old package\n' > "$work/legacy/s-ui/sui"
tar -czf "$work/legacy.tar.gz" -C "$work/legacy" s-ui
if bash "$repo/paired-release/install-s-ui-paired.sh" "$work/legacy.tar.gz"; then echo 'Accepted a legacy overwrite package' >&2; exit 1; fi
sha256sum -c "$work/before.txt"
echo 'Independent installer isolation, rollback and legacy-package rejection passed.'
