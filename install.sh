#!/usr/bin/env bash
# Build and atomically install the Linux host binary. No credentials are changed.
set -euo pipefail

usage() {
  printf 'Usage: %s [INSTALL_DIR]\n' "${0##*/}"
  printf 'Build slkt and replace INSTALL_DIR/slkt (default: $HOME/.local/bin/slkt).\n'
  printf 'Environment: GO selects the Go executable; VERSION overrides the shared development build label.\n'
  printf 'Without local Go, try Podman then Docker using SLKT_GO_IMAGE (default: docker.io/library/golang:1.26).\n'
}

# Wait on a child so INT/TERM traps run promptly, including during a long pull or
# build. A foreground container client would otherwise defer Bash signal traps.
run_tracked() {
  local slkt_status=0
  "$@" <&0 &
  slkt_child_pid=$!
  wait "$slkt_child_pid" || slkt_status=$?
  slkt_child_pid=
  return "$slkt_status"
}

image_was_present() {
  local slkt_previous_id
  while IFS= read -r slkt_previous_id; do
    if [[ ${slkt_previous_id#sha256:} == ${1#sha256:} ]]; then
      return 0
    fi
  done <<< "$slkt_images_before"
  return 1
}

cleanup() {
  local slkt_exit_status=$? slkt_owner slkt_cleanup_id
  trap - EXIT
  trap '' INT TERM
  set +e

  if [[ -n $slkt_child_pid ]]; then
    kill -TERM "$slkt_child_pid" 2>/dev/null
  fi
  if [[ -n $slkt_container_name ]]; then
    # The random name also covers interruption before --cidfile is written.
    # Verify our label before force-removing a container with that name.
    if slkt_owner=$(timeout --kill-after=2 10 "$slkt_runtime" container inspect --format \
        '{{ index .Config.Labels "dev.slakt.install-run" }}' "$slkt_container_name" 2>/dev/null); then
      if [[ $slkt_owner == "$slkt_run_token" ]]; then
        if ! timeout --kill-after=2 10 "$slkt_runtime" container rm --force --volumes "$slkt_container_name" >/dev/null 2>&1; then
          printf 'Cleanup warning: could not remove container %s.\n' "$slkt_container_name" >&2
        fi
      else
        printf 'Cleanup warning: container %s has a different owner label; retained.\n' "$slkt_container_name" >&2
      fi
    elif ! timeout --kill-after=2 10 "$slkt_runtime" info >/dev/null 2>&1; then
      printf 'Cleanup warning: cannot confirm removal of container %s; runtime unavailable.\n' "$slkt_container_name" >&2
    fi
  fi
  if [[ -n $slkt_child_pid ]]; then
    kill -KILL "$slkt_child_pid" 2>/dev/null
    wait "$slkt_child_pid" 2>/dev/null
  fi

  if [[ $slkt_pull_attempted == true ]]; then
    slkt_cleanup_id=$slkt_image_id
    if [[ -z $slkt_cleanup_id ]]; then
      # A pull can finish just before its client fails or gets interrupted.
      slkt_cleanup_id=$(timeout --kill-after=2 10 "$slkt_runtime" image inspect --format '{{.Id}}' "$slkt_go_image" 2>/dev/null)
    fi
    if [[ $slkt_cleanup_id =~ ^(sha256:)?[[:xdigit:]]{64}$ ]]; then
      if ! image_was_present "$slkt_cleanup_id"; then
        # Do not force image deletion or prune existing parent images. The
        # runtime refuses removal if another container now needs this image.
        if ! timeout --kill-after=2 15 "$slkt_runtime" image rm --no-prune "$slkt_cleanup_id" >/dev/null 2>&1; then
          printf 'Cleanup warning: builder image %s was retained (in use or removal failed).\n' "$slkt_cleanup_id" >&2
        fi
      fi
    elif ! timeout --kill-after=2 10 "$slkt_runtime" info >/dev/null 2>&1; then
      printf 'Cleanup warning: cannot check builder image cleanup; runtime unavailable.\n' >&2
    fi
  fi
  if ! rm -rf -- "$slkt_stage"; then
    printf 'Cleanup warning: could not remove staging directory %s.\n' "$slkt_stage" >&2
  fi
  exit "$slkt_exit_status"
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
slkt_script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
slkt_repo_dir=$slkt_script_dir
slkt_version=$(bash "$slkt_repo_dir/scripts/version.sh")
bash "$slkt_repo_dir/scripts/check-version.sh"
slkt_go_image=${SLKT_GO_IMAGE:-docker.io/library/golang:1.26}
if [[ -z $slkt_install_dir ]]; then
  printf 'Install directory must not be empty.\n' >&2
  exit 2
fi
if [[ $(uname -s) != Linux ]]; then
  printf 'This installer currently supports Linux.\n' >&2
  exit 1
fi

slkt_runtime=
slkt_use_local_go=false
slkt_goarm=
if command -v -- "$slkt_go" >/dev/null 2>&1 && \
    slkt_host_os=$("$slkt_go" env GOHOSTOS) && \
    slkt_host_arch=$("$slkt_go" env GOHOSTARCH); then
  slkt_use_local_go=true
else
  if ! command -v timeout >/dev/null 2>&1; then
    printf 'Container builds require the Linux coreutils timeout command.\n' >&2
    exit 1
  fi
  for slkt_candidate in podman docker; do
    if command -v "$slkt_candidate" >/dev/null 2>&1 && \
        timeout --kill-after=2 10 "$slkt_candidate" info >/dev/null 2>&1; then
      slkt_runtime=$slkt_candidate
      break
    fi
  done
  if [[ -z $slkt_runtime ]]; then
    printf 'Local Go is unavailable and no usable Podman or Docker runtime was found.\nInstall Go 1.26+ or start a local container runtime and try again.\n' >&2
    exit 1
  fi
  slkt_host_os=linux
  case $(uname -m) in
    x86_64|amd64) slkt_host_arch=amd64 ;;
    aarch64|arm64) slkt_host_arch=arm64 ;;
    armv7l) slkt_host_arch=arm; slkt_goarm=7 ;;
    armv6l) slkt_host_arch=arm; slkt_goarm=6 ;;
    i386|i486|i586|i686) slkt_host_arch=386 ;;
    ppc64|ppc64le|s390x|riscv64) slkt_host_arch=$(uname -m) ;;
    loongarch64) slkt_host_arch=loong64 ;;
    *) printf 'Unsupported container build architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
  esac
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
slkt_child_pid=
slkt_container_name=
slkt_run_token=${slkt_stage##*/}
slkt_image_id=
slkt_images_before=
slkt_pull_attempted=false
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'Building slkt for %s/%s…\n' "$slkt_host_os" "$slkt_host_arch"
if [[ $slkt_use_local_go == true ]]; then
  CGO_ENABLED=0 GOOS="$slkt_host_os" GOARCH="$slkt_host_arch" \
    run_tracked "$slkt_go" -C "$slkt_repo_dir" build -trimpath \
    -ldflags "-s -w -X main.version=$slkt_version" \
    -o "$slkt_stage/slkt" ./cmd/slkt
else
  printf 'Local Go unavailable; building with %s using %s.\n' "$slkt_runtime" "$slkt_go_image"
  # Copy only the module and Go source. No host mounts, credentials, persistent
  # volumes, SELinux relabeling, or root-owned host files are needed.
  tar -C "$slkt_repo_dir" -cf "$slkt_stage/source.tar" go.mod go.sum cmd internal
  if ! slkt_image_id=$(timeout --kill-after=2 10 "$slkt_runtime" image inspect --format '{{.Id}}' "$slkt_go_image" 2>/dev/null); then
    slkt_images_before=$(timeout --kill-after=2 10 "$slkt_runtime" image ls --quiet --no-trunc)
    slkt_pull_attempted=true
    run_tracked "$slkt_runtime" pull "$slkt_go_image" >&2
    slkt_image_id=$(timeout --kill-after=2 10 "$slkt_runtime" image inspect --format '{{.Id}}' "$slkt_go_image")
  fi
  if [[ ! $slkt_image_id =~ ^(sha256:)?[[:xdigit:]]{64}$ ]]; then
    printf 'Container runtime returned an invalid builder image ID.\n' >&2
    exit 1
  fi
  slkt_container_name=slkt-build-${slkt_stage##*.}
  run_tracked "$slkt_runtime" run --rm --pull=never \
    --name "$slkt_container_name" --cidfile "$slkt_stage/container.id" \
    --label "dev.slakt.install-run=$slkt_run_token" --interactive \
    --env CGO_ENABLED=0 --env "GOOS=$slkt_host_os" --env "GOARCH=$slkt_host_arch" \
    --env "GOARM=$slkt_goarm" --env GOCACHE=/tmp/go-build --env GOMODCACHE=/tmp/go-mod \
    "$slkt_image_id" sh -c '
      set -eu
      mkdir -p /tmp/slkt-source
      tar -xf - -C /tmp/slkt-source
      go -C /tmp/slkt-source build -trimpath -ldflags "-s -w -X main.version=$1" \
        -o /tmp/slkt ./cmd/slkt
      cat /tmp/slkt
    ' slkt-build "$slkt_version" < "$slkt_stage/source.tar" > "$slkt_stage/slkt"
fi
chmod 0755 -- "$slkt_stage/slkt"
slkt_built_version=$("$slkt_stage/slkt" --version)
if [[ $slkt_built_version != "slkt version $slkt_version" ]]; then
  printf 'Built binary has an unexpected version: %s\n' "$slkt_built_version" >&2
  exit 1
fi
printf '%s\n' "$slkt_built_version"
# Another chat may edit this checkout while compilation is running.
VERSION="$slkt_version" bash "$slkt_repo_dir/scripts/check-version.sh"
mv -fT -- "$slkt_stage/slkt" "$slkt_target"
if [[ $("$slkt_target" --version) != "$slkt_built_version" ]]; then
  printf 'Installed version verification failed.\n' >&2
  exit 1
fi
printf 'Installed %s\n' "$slkt_target"

slkt_on_path=$(command -v slkt || true)
if [[ -z $slkt_on_path || ! $slkt_on_path -ef $slkt_target ]]; then
  if [[ -n $slkt_on_path ]]; then
    printf 'Your PATH currently selects %s.\n' "$slkt_on_path"
  fi
  printf 'To use this installation, add its directory first in PATH:\n  export PATH=%q:"$PATH"\n' "$slkt_install_dir"
fi
