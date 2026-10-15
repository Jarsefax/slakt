#!/usr/bin/env bash
# Shared build label for Make and the installer; works without Git too.
set -euo pipefail

slkt_repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
slkt_version=${VERSION:-}
if [[ -z $slkt_version ]]; then
  slkt_version=$(cat "$slkt_repo_dir/cmd/slkt/VERSION")-dev
  if [[ -e $slkt_repo_dir/.git ]] && \
      slkt_revision=$(git -C "$slkt_repo_dir" rev-parse --short=12 HEAD 2>/dev/null); then
    slkt_version+=+$slkt_revision
    if [[ -n $(git -C "$slkt_repo_dir" status --porcelain 2>/dev/null) ]]; then
      slkt_version+=.dirty
    fi
  fi
fi
if [[ ! $slkt_version =~ ^[[:alnum:]._+-]+$ ]]; then
  printf 'VERSION may contain only letters, numbers, dots, underscores, pluses, and hyphens.\n' >&2
  exit 2
fi
printf '%s\n' "$slkt_version"
