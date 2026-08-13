#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${1:-$repo_root/docs/assets/images/screenshots}"
app_bin="${APP_BIN:-hyprmoncfg}"
terminal_bin="${TERMINAL_BIN:-ghostty}"
window_class="${WINDOW_CLASS:-TUI.float}"
font_size="${FONT_SIZE:-12}"
light_theme="${LIGHT_THEME:-ruby-llm}"
dark_theme="${DARK_THEME:-ruby-llm-dark}"

mkdir -p "$output_dir"

for cmd in "$app_bin" "$terminal_bin" hyprctl grim jq wtype; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing: $cmd" >&2
    exit 1
  fi
done

theme_file() {
  local theme="$1"
  local candidate
  for candidate in \
    "$HOME/.config/omarchy/themes/$theme/ghostty.conf" \
    "/usr/share/omarchy/themes/$theme/ghostty.conf"; do
    if [[ -f "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  printf 'Ghostty theme not found: %s\n' "$theme" >&2
  return 1
}

wait_for_client() {
  local title="$1"
  for _ in $(seq 1 80); do
    local client
    client="$(hyprctl -j clients | jq -c --arg title "$title" '.[] | select(.title == $title)' | head -n1)"
    if [[ -n "$client" ]]; then
      printf '%s\n' "$client"
      return 0
    fi
    sleep 0.15
  done
  return 1
}

focused_monitor() {
  hyprctl -j monitors | jq -c '((map(select(.focused)) | .[0]) // .[0])'
}

fit_and_center_window() {
  local address="$1"
  local monitor
  monitor="$(focused_monitor)"

  local monitor_x monitor_y monitor_w monitor_h monitor_scale
  monitor_x="$(printf '%s' "$monitor" | jq -r '.x')"
  monitor_y="$(printf '%s' "$monitor" | jq -r '.y')"
  monitor_w="$(printf '%s' "$monitor" | jq -r '.width')"
  monitor_h="$(printf '%s' "$monitor" | jq -r '.height')"
  monitor_scale="$(printf '%s' "$monitor" | jq -r '.scale')"

  local logical_w logical_h target_w target_h
  logical_w="$(awk -v w="$monitor_w" -v s="$monitor_scale" 'BEGIN { printf "%d", w / s }')"
  logical_h="$(awk -v h="$monitor_h" -v s="$monitor_scale" 'BEGIN { printf "%d", h / s }')"
  target_w=$((logical_w * 5 / 10))
  target_h=$((logical_h * 5 / 10))

  hyprctl eval "hl.dispatch(hl.dsp.window.resize({ x = $target_w, y = $target_h, relative = false, window = \"address:$address\" }))" >/dev/null
  hyprctl eval "hl.dispatch(hl.dsp.window.center({ window = \"address:$address\" }))" >/dev/null
  hyprctl eval "hl.dispatch(hl.dsp.focus({ window = \"address:$address\" }))" >/dev/null
}

capture_state() {
  local name="$1"
  local theme="$2"
  local key_action="${3:-}"
  local title="hyprmoncfg-shot-$name"
  local screenshot="$output_dir/$name.png"
  local colors
  colors="$(theme_file "$theme")"

  env -u NO_COLOR -u FORCE_COLOR -u CLICOLOR_FORCE \
    CLICOLOR=1 COLORTERM=truecolor TERM=xterm-256color \
    "$terminal_bin" \
      --class="$window_class" \
      --title="$title" \
      --font-size="$font_size" \
      --background-opacity=1 \
      --config-file="$colors" \
      -e "$app_bin" >/dev/null 2>&1 &

  local client
  client="$(wait_for_client "$title")"
  local address
  address="$(printf '%s' "$client" | jq -r '.address')"

  fit_and_center_window "$address"
  sleep 1

  if [[ -n "$key_action" ]]; then
    eval "$key_action"
    sleep 0.8
  fi

  client="$(hyprctl -j clients | jq -c --arg address "$address" '.[] | select(.address == $address)' | head -n1)"
  local x y w h border
  x="$(printf '%s' "$client" | jq -r '.at[0]')"
  y="$(printf '%s' "$client" | jq -r '.at[1]')"
  w="$(printf '%s' "$client" | jq -r '.size[0]')"
  h="$(printf '%s' "$client" | jq -r '.size[1]')"
  border="$(hyprctl getoption general:border_size -j | jq -r '.int')"

  grim -g "$((x - border)),$((y - border)) $((w + 2 * border))x$((h + 2 * border))" "$screenshot"
  hyprctl dispatch closewindow "address:$address" >/dev/null 2>&1 || true
}

capture_themed() {
  local theme="$1"
  local suffix="$2"

  printf 'Capturing %s...\n' "$theme"
  capture_state "layout${suffix}" "$theme"
  capture_state "save-profile${suffix}" "$theme" "wtype -k s"
  capture_state "profiles${suffix}" "$theme" "wtype -k 2"
  capture_state "workspaces${suffix}" "$theme" "wtype -k 3"
}

capture_themed "$light_theme" "-light"
capture_themed "$dark_theme" "-dark"

cp "$output_dir/layout-light.png" "$output_dir/layout.png"
cp "$output_dir/save-profile-light.png" "$output_dir/save-profile.png"

printf 'Captured screenshots in %s\n' "$output_dir"
