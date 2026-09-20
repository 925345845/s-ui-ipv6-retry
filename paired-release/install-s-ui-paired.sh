#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo "请使用 root 或 sudo 运行。" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64|amd64) package_arch="amd64" ;;
  aarch64|arm64) package_arch="arm64" ;;
  *)
    echo "仅支持 amd64/x86_64 或 arm64/aarch64 VPS。" >&2
    exit 1
    ;;
esac

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
archive="${1:-${script_dir}/s-ui-linux-${package_arch}-paired.tar.gz}"
if [[ ! -f "$archive" ]]; then
  echo "找不到当前架构的安装包：$archive" >&2
  exit 1
fi

tmp_dir="$(mktemp -d /tmp/1s-ui-paired.XXXXXX)"
cleanup() {
  rm -rf -- "$tmp_dir"
}
trap cleanup EXIT

tar -xzf "$archive" -C "$tmp_dir"
src="$tmp_dir/s-ui"
if [[ ! -f "$src/sui" ]]; then
  echo "安装包内缺少 s-ui/sui。" >&2
  exit 1
fi
install -d -m 755 /usr/local/s-ui /usr/local/s-ui/db /usr/local/s-ui/bin
backup=""
if [[ -f /usr/local/s-ui/sui ]]; then
  backup="/usr/local/s-ui/sui.backup.$(date +%Y%m%d%H%M%S)"
  cp -p /usr/local/s-ui/sui "$backup"
  echo "原面板二进制已备份到：$backup"
fi

systemctl stop s-ui 2>/dev/null || true
install -m 755 "$src/sui" /usr/local/s-ui/sui
install -m 755 "$src/s-ui.sh" /usr/local/s-ui/s-ui.sh
install -m 755 "$src/s-ui.sh" /usr/bin/s-ui
if [[ ! -f /etc/systemd/system/s-ui.service ]]; then
  install -m 644 "$src/s-ui.service" /etc/systemd/system/s-ui.service
fi

if [[ -f "$src/sui-agent" ]]; then
  install -m 755 "$src/sui-agent" /usr/local/s-ui/sui-agent
fi
if [[ -f "$src/s-ui-agent.service" && ! -f /etc/systemd/system/s-ui-agent.service ]]; then
  install -m 644 "$src/s-ui-agent.service" /etc/systemd/system/s-ui-agent.service
fi

systemctl daemon-reload
systemctl enable s-ui >/dev/null
if ! systemctl restart s-ui; then
  if [[ -n "$backup" ]]; then
    cp -p "$backup" /usr/local/s-ui/sui
    systemctl restart s-ui || true
    echo "新版本启动失败，已恢复原面板二进制。" >&2
  fi
  exit 1
fi

sleep 3
if ! systemctl is-active --quiet s-ui; then
  journalctl -u s-ui -n 60 --no-pager || true
  if [[ -n "$backup" ]]; then
    systemctl stop s-ui 2>/dev/null || true
    cp -p "$backup" /usr/local/s-ui/sui
    systemctl restart s-ui || true
    echo "新版本未能保持运行，已恢复原面板二进制。" >&2
  fi
  exit 1
fi

echo "安装完成，原数据库保留在 /usr/local/s-ui/db。"
echo "默认访问地址：http://VPS-IP:2095/app/"
echo "进入：入站管理 -> 一键中转 -> 双栈出口。"
