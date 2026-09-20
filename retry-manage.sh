#!/usr/bin/env bash
set -euo pipefail
service=s-ui-ipv6-retry
action="${1:-status}"
case "$action" in
  start|stop|restart|status) systemctl "$action" "$service" ;;
  log) journalctl -u "$service" -n 100 --no-pager ;;
  update)
    tmp="$(mktemp /tmp/ipv6-retry-update.XXXXXX)"
    trap 'rm -f -- "$tmp"' EXIT
    curl -4 -fsSL --retry 3 https://raw.githubusercontent.com/925345845/s-ui-ipv6-retry/main/paired-release/install-s-ui-paired-online.sh -o "$tmp"
    bash "$tmp"
    ;;
  *) echo '用法：s-ui-ipv6-retry {start|stop|restart|status|log|update}' >&2; exit 2 ;;
esac
