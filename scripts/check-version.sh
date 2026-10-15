#!/usr/bin/env bash
# A delivered version is tied to one set of application/build inputs.
set -euo pipefail
slkt_repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
slkt_base=$(cat "$slkt_repo_dir/cmd/slkt/VERSION")
if [[ ! $slkt_base =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  printf 'cmd/slkt/VERSION must be a numeric major.minor.patch version.\n' >&2
  exit 2
fi
slkt_record=releases/$slkt_base.sha256
if [[ ! -f $slkt_repo_dir/$slkt_record ]]; then
  printf 'Version %s has no source record. Run make prepare-update BUMP=patch (or minor) after finishing the update.\n' "$slkt_base" >&2
  exit 2
fi
slkt_expected=$(cat "$slkt_repo_dir/$slkt_record")
slkt_actual=$(bash "$slkt_repo_dir/scripts/source-fingerprint.sh")
if [[ ! $slkt_expected =~ ^[0-9a-f]{64}$ || $slkt_expected != "$slkt_actual" ]]; then
  printf 'Application source changed since version %s was prepared. Run make prepare-update BUMP=patch (or minor); do not reuse this version.\n' "$slkt_base" >&2
  exit 2
fi

# A version label override must not bypass the canonical delivery version.
if [[ -n ${VERSION:-} && ${VERSION%%[-+]*} != "$slkt_base" ]]; then
  printf 'VERSION must use the prepared base version %s for installation/release.\n' "$slkt_base" >&2
  exit 2
fi

# Verify committed records against their first appearance, even if a later
# commit tried to rewrite one. Archives still enforce the shipped source hash.
if [[ -e $slkt_repo_dir/.git ]]; then
  command -v git >/dev/null || { printf 'Git is required to check version history in a checkout.\n' >&2; exit 2; }
  slkt_first=$(git -C "$slkt_repo_dir" log --diff-filter=A --format=%H -- "$slkt_record" | tail -n 1)
  if [[ -n $slkt_first ]]; then
    slkt_original=$(git -C "$slkt_repo_dir" show "$slkt_first:$slkt_record")
    if [[ $slkt_expected != "$slkt_original" ]]; then
      printf 'Release record %s is immutable; prepare a new version.\n' "$slkt_base" >&2
      exit 2
    fi
  fi
  slkt_previous=$(git -C "$slkt_repo_dir" show HEAD:cmd/slkt/VERSION 2>/dev/null || true)
  if [[ -n $slkt_previous && $slkt_previous != "$slkt_base" && \
        $(printf '%s\n%s\n' "$slkt_base" "$slkt_previous" | sort -V | head -n 1) == "$slkt_base" ]]; then
    printf 'Version must increase from %s, not move back to %s.\n' "$slkt_previous" "$slkt_base" >&2
    exit 2
  fi
fi
