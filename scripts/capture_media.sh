#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

for cmd in hyprmoncfg vhs; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing: $cmd" >&2
    exit 1
  fi
done

(
  cd "$repo_root"
  env -u NO_COLOR -u FORCE_COLOR -u CLICOLOR_FORCE CLICOLOR=1 COLORTERM=truecolor TERM=xterm-256color vhs scripts/demo.tape
)

"$repo_root/scripts/capture_screenshots.sh" "$@"
