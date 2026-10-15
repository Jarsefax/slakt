#!/usr/bin/env bash
# Hash application and build inputs, including untracked files, in stable order.
# Tests/docs and version records are excluded so they do not require a release.
set -euo pipefail
slkt_repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
cd -- "$slkt_repo_dir"
{
  printf '%s\0' go.mod go.sum Makefile install.sh
  find cmd internal -type f ! -name '*_test.go' ! -path 'cmd/slkt/VERSION' -print0
  find scripts -maxdepth 1 -type f -name '*.sh' -print0
} | LC_ALL=C sort -zu | xargs -0 sha256sum -- | sha256sum | cut -d ' ' -f 1
