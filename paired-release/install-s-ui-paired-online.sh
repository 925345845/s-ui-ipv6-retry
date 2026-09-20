#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo "请使用 root 或 sudo 运行。" >&2
  exit 1
fi
case "$(uname -m)" in
  x86_64|amd64) package_arch="amd64" ;;
  aarch64|arm64) package_arch="arm64" ;;
  *) echo "仅支持 Linux amd64/x86_64 或 arm64/aarch64。" >&2; exit 1 ;;
esac
[[ "$(uname -s)" == Linux ]] || { echo "此安装器仅支持 Linux。" >&2; exit 1; }

release_version="${S_UI_PAIRED_VERSION:-v1.1.0}"
[[ "$release_version" =~ ^v[0-9][A-Za-z0-9._-]*$ ]] || { echo "无效版本号。" >&2; exit 1; }
[[ "$release_version" != v1.0.* ]] || { echo "v1.0.x 会覆盖原面板，已停止提供安装，请使用 v1.1.0 或更新版本。" >&2; exit 1; }
base_url="https://github.com/925345845/s-ui-ipv6-retry/releases/download/${release_version}"
tmp_dir="$(mktemp -d /tmp/s-ui-ipv6-retry.XXXXXX)"
trap 'rm -rf -- "$tmp_dir"' EXIT
archive="s-ui-linux-${package_arch}.tar.gz"
installer="install-s-ui-paired.sh"

echo "正在下载 S-UI IPv6 Retry ${release_version} (${package_arch})..."
for file in "$archive" "$installer" SHA256SUMS.txt; do
  curl -4 -fL --retry 3 --connect-timeout 15 --max-time 600 \
    "$base_url/$file" -o "$tmp_dir/$file"
done
for file in "$archive" "$installer"; do
  expected="$(awk -v name="$file" '$2 == name { print $1 }' "$tmp_dir/SHA256SUMS.txt")"
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { echo "缺少有效校验值：$file" >&2; exit 1; }
  (cd "$tmp_dir"; printf '%s  %s\n' "$expected" "$file" | sha256sum --check --status)
done
bash "$tmp_dir/$installer" "$tmp_dir/$archive"
