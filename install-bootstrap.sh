#!/usr/bin/env bash
set -euo pipefail
connect=""; panel=""; token=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -y|--minimal|--managed-client) shift ;;
    --connect) connect="${2:?缺少连接地址}"; shift 2 ;;
    --controller) panel="${2:?缺少控制端地址}"; shift 2 ;;
    --agent-token) token="${2:?缺少令牌}"; shift 2 ;;
    v[0-9]*|[0-9]*) export S_UI_PAIRED_VERSION="${1#v}"; export S_UI_PAIRED_VERSION="v$S_UI_PAIRED_VERSION"; shift ;;
    *) echo "不支持此参数：$1。此安装器只安装独立 IPv6 Retry。" >&2; exit 2 ;;
  esac
done
work="$(mktemp -d /tmp/ipv6-retry-bootstrap.XXXXXX)"
trap 'rm -rf -- "$work"' EXIT
curl -4 -fsSL --retry 3 https://raw.githubusercontent.com/925345845/s-ui-ipv6-retry/main/paired-release/install-s-ui-paired-online.sh -o "$work/install.sh"
bash "$work/install.sh"
if [[ -n "$connect" || -n "$panel" ]]; then
  curl -4 -fsSL --retry 3 https://raw.githubusercontent.com/925345845/s-ui-ipv6-retry/main/install-agent.sh -o "$work/agent.sh"
  if [[ -n "$connect" ]]; then bash "$work/agent.sh" --connect "$connect"; else bash "$work/agent.sh" --panel "$panel" --token "$token"; fi
fi
