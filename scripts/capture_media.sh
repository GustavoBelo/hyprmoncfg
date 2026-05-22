#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin_dir="$(mktemp -d)"

cleanup() {
  rm -rf "$bin_dir"
}
trap cleanup EXIT

for cmd in go vhs; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing: $cmd" >&2
    exit 1
  fi
done

(
  cd "$repo_root"
  go build -o "$bin_dir/hyprmoncfg" ./cmd/hyprmoncfg
)

(
  cd "$repo_root"
  PATH="$bin_dir:$PATH" vhs scripts/demo.tape
)

APP_BIN="$bin_dir/hyprmoncfg" "$repo_root/scripts/capture_screenshots.sh" "$@"
