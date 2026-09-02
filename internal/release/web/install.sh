#!/usr/bin/env bash
set -euo pipefail

release_base="${HUSHI_RELEASE_BASE_URL:-https://hushi.icooler.opik.net/api/v1/server/releases/latest}"
install_dir="${HUSHI_INSTALL_DIR:-${HOME:?HOME is required}/.local/bin}"
release_base="${release_base%/}"

target="${install_dir}/hushi"

ask_yes_no() {
  prompt="$1"
  if [ "${HUSHI_YES:-}" = "1" ] || [ "${HUSHI_YES:-}" = "true" ]; then
    return 0
  fi
  if [ ! -r /dev/tty ] || [ ! -w /dev/tty ]; then
    echo "An interactive terminal is required. Set HUSHI_YES=1 for a scripted install." >&2
    return 1
  fi
  printf '%s [y/N] ' "$prompt" > /dev/tty
  answer=""
  IFS= read -r answer < /dev/tty || return 1
  case "$answer" in
    y|Y|yes|YES|Yes) return 0 ;;
    *) return 1 ;;
  esac
}

if [ -e "$target" ] && [ ! -f "$target" ]; then
  echo "Install target exists but is not a regular file: $target" >&2
  exit 1
fi

if [ -f "$target" ]; then
  operation="update"
  if ! ask_yes_no "Hushi is already installed at $target. Update it now?"; then
    echo "Update cancelled." >&2
    exit 0
  fi
else
  operation="install"
  if ! ask_yes_no "Install Hushi server to $target?"; then
    echo "Installation cancelled." >&2
    exit 0
  fi
fi

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
mv "${tmp_dir}/${asset}" "${target}"
if [ "$operation" = "update" ]; then
  echo "Updated ${target}" >&2
else
  echo "Installed ${target}" >&2
fi

auto_update="${HUSHI_AUTO_UPDATE:-}"
if [ -z "$auto_update" ]; then
  if [ -n "${HUSHI_YES:-}" ]; then
    auto_update="off"
  elif ask_yes_no "Enable automatic Hushi server updates?"; then
    auto_update="on"
  else
    auto_update="off"
  fi
else
  case "$auto_update" in
    on|ON|true|TRUE|1|yes|YES) auto_update="on" ;;
    off|OFF|false|FALSE|0|no|NO) auto_update="off" ;;
    *) echo "HUSHI_AUTO_UPDATE must be on or off" >&2; exit 1 ;;
  esac
fi

if [ "$operation" = "install" ]; then
  "${target}" setup "$@"
else
  if ! "${target}" service refresh; then
    echo "No managed Hushi service found; registering it now..." >&2
    "${target}" setup "$@"
  fi
fi

"${target}" config set auto-update "$auto_update"
echo "Automatic updates: ${auto_update}." >&2
