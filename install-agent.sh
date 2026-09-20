#!/usr/bin/env bash

set -euo pipefail

repo="925345845/s-ui-ipv6-retry"
panel_url=""
token=""
version=""
insecure="false"
connect_url=""

usage() {
    echo "Usage: install-agent.sh --connect CONTROLLER_API [--version VERSION] [--insecure]"
    echo "   or: install-agent.sh --panel URL --token TOKEN [--version VERSION] [--insecure]"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --panel)
            panel_url="${2:-}"
            shift 2
            ;;
        --token)
            token="${2:-}"
            shift 2
            ;;
        --connect)
            connect_url="${2:-}"
            shift 2
            ;;
        --version)
            version="${2:-}"
            shift 2
            ;;
        --insecure)
            insecure="true"
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ ${EUID} -ne 0 ]]; then
    echo "Run this installer as root." >&2
    exit 1
fi
if [[ "$(uname -s)" != "Linux" ]] || ! command -v systemctl >/dev/null 2>&1; then
    echo "The 1S-UI Agent installer requires Linux with systemd." >&2
    exit 1
fi
if [[ -n "$connect_url" ]]; then
    if [[ "$connect_url" != http://* && "$connect_url" != https://* ]] || [[ "$connect_url" != *"#"* ]]; then
        echo "Invalid controller connection API or one-time address." >&2
        exit 1
    fi
else
    if [[ ! "$panel_url" =~ ^https?://[^[:space:]\'\"\\]+$ ]]; then
        echo "Invalid panel URL." >&2
        exit 1
    fi
    if [[ ! "$token" =~ ^[A-Za-z0-9_-]{32,128}$ ]]; then
        echo "Invalid enrollment token." >&2
        exit 1
    fi
fi

resolve_connection() {
    [[ -n "$connect_url" ]] || return 0
    local endpoint code response node_name
    endpoint="${connect_url%%#*}"
    code="${connect_url#*#}"
    if [[ ! "$endpoint" =~ ^https?://[^[:space:]\'\"\\]+/agent/v1/(pair|enroll)$ ]] || [[ ! "$code" =~ ^[A-Za-z0-9_-]{32,128}$ ]]; then
        echo "Invalid controller connection API or one-time address." >&2
        return 1
    fi
    node_name=$(hostname 2>/dev/null | tr -d '\r\n' | sed 's/["\\]//g' | cut -c1-80)
    [[ -n "$node_name" ]] || node_name="managed-server"
    local curl_args=(--fail --silent --show-error --max-time 20)
    [[ "$insecure" == "true" ]] && curl_args+=(--insecure)
    echo "Connecting to the 1S-UI controller..."
    response=$(curl "${curl_args[@]}" -H 'Content-Type: application/json' --data "{\"code\":\"${code}\",\"name\":\"${node_name}\"}" "$endpoint") || {
        echo "The controller rejected or could not process the connection API." >&2
        return 1
    }
    panel_url=$(printf '%s' "$response" | sed -nE 's/.*"panel_url"[[:space:]]*:[[:space:]]*"([^\"]+)".*/\1/p' | head -n1)
    token=$(printf '%s' "$response" | sed -nE 's/.*"token"[[:space:]]*:[[:space:]]*"([^\"]+)".*/\1/p' | head -n1)
    if [[ ! "$panel_url" =~ ^https?://[^[:space:]\'\"\\]+$ ]] || [[ ! "$token" =~ ^[A-Za-z0-9_-]{32,128}$ ]]; then
        echo "The controller returned an invalid pairing response." >&2
        return 1
    fi
}

case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    i386|i486|i586|i686) arch="386" ;;
    aarch64|arm64) arch="arm64" ;;
    armv7*|armhf) arch="armv7" ;;
    armv6*) arch="armv6" ;;
    armv5*) arch="armv5" ;;
    s390x) arch="s390x" ;;
    *)
        echo "Unsupported architecture: $(uname -m)" >&2
        exit 1
        ;;
esac

if [[ -z "$version" ]]; then
    version=$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" | sed -nE 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/p' | head -n1)
fi
[[ "$version" == v* ]] || version="v${version}"
if [[ ! "$version" =~ ^v[0-9A-Za-z._-]+$ ]]; then
    echo "Invalid release version." >&2
    exit 1
fi

tmp_dir=$(mktemp -d /tmp/1s-ui-agent.XXXXXX)
trap 'rm -rf "$tmp_dir"' EXIT
archive="$tmp_dir/s-ui.tar.gz"
url="https://github.com/${repo}/releases/download/${version}/s-ui-linux-${arch}.tar.gz"

echo "Downloading 1S-UI Agent ${version} for ${arch}..."
curl --fail --location --retry 3 --output "$archive" "$url"
tar -xzf "$archive" -C "$tmp_dir" s-ui/sui-agent s-ui/s-ui-ipv6-retry-agent.service

install -d -m 0755 /usr/local/s-ui-ipv6-retry /etc/default /etc/systemd/system
install -m 0755 "$tmp_dir/s-ui/sui-agent" /usr/local/s-ui-ipv6-retry/sui-agent
install -m 0644 "$tmp_dir/s-ui/s-ui-ipv6-retry-agent.service" /etc/systemd/system/s-ui-ipv6-retry-agent.service
resolve_connection
umask 077
{
    printf 'SUI_AGENT_PANEL=%s\n' "$panel_url"
    printf 'SUI_AGENT_TOKEN=%s\n' "$token"
    printf 'SUI_AGENT_INTERVAL=15s\n'
    printf 'SUI_AGENT_INSECURE=%s\n' "$insecure"
    printf 'SUI_AGENT_LOCAL_SOCKET=/run/s-ui-ipv6-retry/control.sock\n'
} > /etc/default/s-ui-ipv6-retry-agent

echo "Validating the panel connection..."
agent_check=(
    /usr/local/s-ui-ipv6-retry/sui-agent
    --panel "$panel_url"
    --token "$token"
    --interval 15s
    --local-socket /run/s-ui-ipv6-retry/control.sock
    --once
)
if [[ "$insecure" == "true" ]]; then
    agent_check+=(--insecure)
fi
"${agent_check[@]}"
systemctl daemon-reload
systemctl enable --now s-ui-ipv6-retry-agent
systemctl is-active --quiet s-ui-ipv6-retry-agent
echo "1S-UI Agent is connected and running."
