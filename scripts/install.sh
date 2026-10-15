#!/usr/bin/env bash
# Build and atomically install the Linux host binary. No credentials are changed.
set -euo pipefail

usage() {
  printf 'Usage: %s [INSTALL_DIR]\n' "${0##*/}"
  printf 'Build slkt and replace INSTALL_DIR/slkt (default: $HOME/.local/bin/slkt).\n'
  printf 'Environment: GO selects the Go executable; VERSION defaults to 0.1.0-dev.\n'
}

if [[ $# -gt 1 ]]; then
  usage >&2
  exit 2
fi
if [[ ${1:-} == --help || ${1:-} == -h ]]; then
  usage
  exit 0
fi

slkt_install_dir=${1-"$HOME/.local/bin"}
slkt_go=${GO:-go}
slkt_version=${VERSION:-0.1.0-dev}
if [[ -z $slkt_install_dir ]]; then
  printf 'Install directory must not be empty.\n' >&2
  exit 2
fi
if [[ ! $slkt_version =~ ^[[:alnum:]._+-]+$ ]]; then
  printf 'VERSION may contain only letters, numbers, dots, underscores, pluses, and hyphens.\n' >&2
  exit 2
fi
if ! command -v -- "$slkt_go" >/dev/null 2>&1; then
  printf 'Go 1.26 or newer is required to build slkt; could not find %s.\n' "$slkt_go" >&2
  exit 1
fi

slkt_script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
slkt_repo_dir=$(cd -- "$slkt_script_dir/.." && pwd -P)
slkt_host_os=$("$slkt_go" env GOHOSTOS)
slkt_host_arch=$("$slkt_go" env GOHOSTARCH)
if [[ $slkt_host_os != linux ]]; then
  printf 'This installer currently supports Linux.\n' >&2
  exit 1
fi

mkdir -p -- "$slkt_install_dir"
slkt_install_dir=$(cd -- "$slkt_install_dir" && pwd -P)
slkt_target=$slkt_install_dir/slkt
if [[ -d $slkt_target ]]; then
  printf 'Cannot replace a directory: %s\n' "$slkt_target" >&2
  exit 1
fi

# Stage on the destination filesystem so rename replaces the old executable
# atomically, including when it is still in use by an existing TUI session.
slkt_stage=$(mktemp -d "$slkt_install_dir/.slkt-install.XXXXXX")
trap 'rm -rf -- "$slkt_stage"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'Building slkt for %s/%s…\n' "$slkt_host_os" "$slkt_host_arch"
CGO_ENABLED=0 GOOS="$slkt_host_os" GOARCH="$slkt_host_arch" \
  "$slkt_go" -C "$slkt_repo_dir" build -trimpath \
  -ldflags "-s -w -X main.version=$slkt_version" \
  -o "$slkt_stage/slkt" ./cmd/slkt
chmod 0755 -- "$slkt_stage/slkt"
"$slkt_stage/slkt" --version
mv -fT -- "$slkt_stage/slkt" "$slkt_target"
printf 'Installed %s\n' "$slkt_target"

slkt_on_path=$(command -v slkt || true)
if [[ -z $slkt_on_path || ! $slkt_on_path -ef $slkt_target ]]; then
  if [[ -n $slkt_on_path ]]; then
    printf 'Your PATH currently selects %s.\n' "$slkt_on_path"
  fi
  printf 'To use this installation, add its directory first in PATH:\n  export PATH=%q:"$PATH"\n' "$slkt_install_dir"
fi
