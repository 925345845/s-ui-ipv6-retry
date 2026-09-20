#!/usr/bin/env bash
set -euo pipefail
[[ ${EUID:-$(id -u)} -eq 0 ]] || { echo "请使用 root 或 sudo 运行。" >&2; exit 1; }
[[ "$(uname -s)" == Linux ]] || { echo "仅支持 Linux。" >&2; exit 1; }
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "不支持当前 CPU 架构。" >&2; exit 1 ;;
esac
archive="${1:-$(dirname "${BASH_SOURCE[0]}")/s-ui-linux-${arch}.tar.gz}"
[[ -f "$archive" ]] || { echo "找不到安装包：$archive" >&2; exit 1; }
for cmd in tar systemctl ip; do command -v "$cmd" >/dev/null || { echo "缺少命令：$cmd（请先安装 iproute2/systemd）。" >&2; exit 1; }; done
# A staging root is used by the isolated installer regression test.
root="${S_UI_RETRY_ROOT:-}"
[[ -z "$root" || "$root" == /* ]] || { echo "安装根目录必须为绝对路径" >&2; exit 1; }
base="$root/usr/local/s-ui-ipv6-retry"
unit="$root/etc/systemd/system/s-ui-ipv6-retry.service"
manager="$root/usr/bin/s-ui-ipv6-retry"
service=s-ui-ipv6-retry
work="$(mktemp -d /tmp/ipv6-retry-install.XXXXXX)"
trap 'rm -rf -- "$work"' EXIT
tar -xzf "$archive" -C "$work"
src="$work/s-ui"
for file in sui retry-manage.sh s-ui-ipv6-retry.service; do
  [[ -f "$src/$file" ]] || { echo "安装包缺少独立运行组件 $file；拒绝安装旧版覆盖包。" >&2; exit 1; }
done
install -d -m 755 "$base" "$base/db" "$base/bin" "$(dirname "$unit")" "$(dirname "$manager")"
backup=""
if [[ -f "$base/sui" ]]; then
  backup="$base/backup-$(date +%Y%m%d%H%M%S)"
  install -d -m 700 "$backup"
  cp -p "$base/sui" "$backup/sui"
  [[ ! -f "$unit" ]] || cp -p "$unit" "$backup/service"
  [[ ! -f "$manager" ]] || cp -p "$manager" "$backup/manager"
fi
was_active=false
if systemctl is-active --quiet "$service"; then was_active=true; fi
systemctl stop "$service" 2>/dev/null || true
rollback() {
  systemctl stop "$service" 2>/dev/null || true
  if [[ -n "$backup" ]]; then
    cp -p "$backup/sui" "$base/sui"
    [[ ! -f "$backup/service" ]] || cp -p "$backup/service" "$unit"
    [[ ! -f "$backup/manager" ]] || cp -p "$backup/manager" "$manager"
    systemctl daemon-reload
    if [[ "$was_active" == true ]]; then systemctl start "$service" || true; fi
    echo "独立工具更新失败，已恢复其旧版本。" >&2
  else
    systemctl disable "$service" 2>/dev/null || true
    echo "独立工具未能启动，请检查 2195/2196 端口及日志。" >&2
  fi
}
trap 'rollback' ERR
install -m 755 "$src/sui" "$base/sui"
install -m 755 "$src/retry-manage.sh" "$manager"
install -m 644 "$src/s-ui-ipv6-retry.service" "$unit"
if [[ -n "$root" ]]; then
  sed -i "s|/usr/local/s-ui-ipv6-retry|$base|g; s|/run/s-ui-ipv6-retry|$root/run/s-ui-ipv6-retry|g" "$unit"
fi
if [[ -f "$src/sui-agent" && -f "$src/s-ui-ipv6-retry-agent.service" ]]; then
  install -m 755 "$src/sui-agent" "$base/sui-agent"
  install -m 644 "$src/s-ui-ipv6-retry-agent.service" "$root/etc/systemd/system/s-ui-ipv6-retry-agent.service"
fi
systemctl daemon-reload
systemctl enable "$service" >/dev/null
systemctl restart "$service"
sleep 3
systemctl is-active --quiet "$service"
trap - ERR
echo "独立工具安装完成：http://服务器IP:2195/app/"
echo "全新数据库：$base/db；服务：s-ui-ipv6-retry。原 s-ui 服务和数据库未修改。"
echo "首次登录使用 admin / admin，登录后请修改密码。"
