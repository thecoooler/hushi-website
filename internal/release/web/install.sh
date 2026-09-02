#!/usr/bin/env bash
set -euo pipefail

release_base="${HUSHI_RELEASE_BASE_URL:-https://hushi.icooler.opik.net/api/v1/server/releases/latest}"
install_dir="${HUSHI_INSTALL_DIR:-${HOME:?HOME is required}/.local/bin}"
release_base="${release_base%/}"

case "$(uname -s)" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *) echo "hushi supports Linux and macOS only" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
esac

asset="hushi-${os}-${arch}"
tmp_dir="$(mktemp -d 2>/dev/null || mktemp -d -t hushi)"
trap 'rm -rf "$tmp_dir"' EXIT

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi

echo "Downloading hushi (${os}/${arch})..." >&2
curl --fail --silent --show-error --location "${release_base}/${asset}" -o "${tmp_dir}/${asset}"
curl --fail --silent --show-error --location "${release_base}/checksums.txt" -o "${tmp_dir}/checksums.txt"

expected="$(awk -v name="${asset}" '$2 == name || $2 == "*" name { print $1; exit }' "${tmp_dir}/checksums.txt")"
if [ -z "${expected}" ]; then
  echo "checksum for ${asset} is missing" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmp_dir}/${asset}" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "${tmp_dir}/${asset}" | awk '{print $1}')"
fi
if [ "${actual}" != "${expected}" ]; then
  echo "checksum verification failed for ${asset}" >&2
  exit 1
fi

mkdir -p "${install_dir}"
chmod 0755 "${tmp_dir}/${asset}"
target="${install_dir}/hushi"
mv "${tmp_dir}/${asset}" "${target}"
echo "Installed ${target}" >&2
exec "${target}" setup "$@"
