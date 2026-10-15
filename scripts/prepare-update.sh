#!/usr/bin/env bash
# Explicit preparation, never an implicit side effect of a build or install.
set -euo pipefail
slkt_repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
slkt_current=$(cat "$slkt_repo_dir/cmd/slkt/VERSION")
if [[ ! $slkt_current =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  printf 'cmd/slkt/VERSION must be a numeric major.minor.patch version.\n' >&2
  exit 2
fi
IFS=. read -r slkt_major slkt_minor slkt_patch <<< "$slkt_current"
case ${1:-patch} in
  patch) slkt_patch=$((slkt_patch + 1)) ;;
  minor) slkt_minor=$((slkt_minor + 1)); slkt_patch=0 ;;
  major) slkt_major=$((slkt_major + 1)); slkt_minor=0; slkt_patch=0 ;;
  *) printf 'BUMP must be patch, minor, or major.\n' >&2; exit 2 ;;
esac
slkt_next=$slkt_major.$slkt_minor.$slkt_patch
slkt_record=$slkt_repo_dir/releases/$slkt_next.sha256
if [[ -e $slkt_record ]]; then
  printf 'Version %s already has a source record; refusing to overwrite it.\n' "$slkt_next" >&2
  exit 2
fi
slkt_fingerprint=$(bash "$slkt_repo_dir/scripts/source-fingerprint.sh")
mkdir -p -- "$slkt_repo_dir/releases"
printf '%s\n' "$slkt_fingerprint" > "$slkt_record"
printf '%s\n' "$slkt_next" > "$slkt_repo_dir/cmd/slkt/VERSION"
printf 'Prepared %s. Include cmd/slkt/VERSION and releases/%s.sha256 with the update.\n' "$slkt_next" "$slkt_next"
