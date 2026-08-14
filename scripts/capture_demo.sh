#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
app_bin="${APP_BIN:-hyprmoncfg}"
demo_profile="${DEMO_PROFILE:-My Setup}"
output_dir="$repo_root/docs/assets/images"
temp_root="$repo_root/.tmp"

for cmd in "$app_bin" vhs ffprobe; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing: $cmd" >&2
    exit 1
  fi
done

mkdir -p "$temp_root"
temp_dir="$(mktemp -d "$temp_root/capture-demo.XXXXXX")"
temp_tape="$temp_dir/demo.tape"
temp_gif="$temp_dir/demo.gif"
temp_mp4="$temp_dir/demo.mp4"

profile_exists() {
  "$app_bin" profiles 2>/dev/null | awk -v name="$demo_profile" '
    NR > 1 {
      line = $0
      sub(/[[:space:]]+[0-9]+[[:space:]].*$/, "", line)
      if (line == name) found = 1
    }
    END { exit !found }
  '
}

demo_profile_existed=false
if profile_exists; then
  demo_profile_existed=true
fi

cleanup_demo_profile() {
  if [[ "$demo_profile_existed" == false ]] && profile_exists; then
    "$app_bin" delete "$demo_profile" >/dev/null
    printf 'Removed demo profile %q.\n' "$demo_profile"
  fi
}

cleanup() {
  cleanup_demo_profile
  rm -f -- "$temp_tape" "$temp_gif" "$temp_mp4"
  rmdir -- "$temp_dir" 2>/dev/null || true
}

wait_for_recording() {
  local file="$1"
  local previous_size=-1
  local stable_checks=0
  local deadline=$((SECONDS + 30))

  while (( SECONDS < deadline )); do
    if [[ -s "$file" ]] && ffprobe -v error "$file" >/dev/null 2>&1; then
      local size
      size="$(stat -c %s "$file")"
      if [[ "$size" == "$previous_size" ]]; then
        ((stable_checks += 1))
      else
        stable_checks=0
      fi
      previous_size="$size"
      if (( stable_checks >= 4 )); then
        return 0
      fi
    fi
    sleep 0.5
  done

  printf 'Recording did not finish: %s\n' "$file" >&2
  return 1
}

trap cleanup EXIT INT TERM

sed \
  -e "s|^Output docs/assets/images/demo.gif$|Output \"$temp_gif\"|" \
  -e "s|^Output docs/assets/images/demo.mp4$|Output \"$temp_mp4\"|" \
  "$repo_root/scripts/demo.tape" >"$temp_tape"

(
  cd "$repo_root"
  env \
    -u NO_COLOR \
    -u FORCE_COLOR \
    -u CLICOLOR_FORCE \
    CLICOLOR=1 \
    COLORTERM=truecolor \
    TERM=xterm-256color \
    vhs "$temp_tape"
)

wait_for_recording "$temp_gif"
wait_for_recording "$temp_mp4"

mkdir -p "$output_dir"
mv -f -- "$temp_gif" "$output_dir/demo.gif"
mv -f -- "$temp_mp4" "$output_dir/demo.mp4"

printf 'Captured demo in %s.\n' "$output_dir"
