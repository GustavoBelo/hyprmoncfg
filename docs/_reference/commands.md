---
title: Commands and Flags
description: Complete CLI and daemon command reference.
nav_order: 1
---

## `hyprmoncfg`

Running `hyprmoncfg` with no arguments opens the TUI.

### Commands

| Command | Description |
|---------|-------------|
| `hyprmoncfg` | Open the TUI |
| `hyprmoncfg tui` | Open the TUI (explicit) |
| `hyprmoncfg monitors` | List connected monitors with hardware details |
| `hyprmoncfg profiles` | List saved profiles |
| `hyprmoncfg status` | Show the active profile, daemon state, and connected displays |
| `hyprmoncfg status --json` | Print the stable status schema as JSON |
| `hyprmoncfg save <name>` | Save current monitor state as a named profile |
| `hyprmoncfg apply <name>` | Apply a saved profile |
| `hyprmoncfg delete <name>` | Delete a saved profile |
| `hyprmoncfg doctor` | Check that Hyprland loads hyprmoncfg's monitor config last |
| `hyprmoncfg doctor --fix` | Add or move that include to the end of the Hyprland config |
| `hyprmoncfg version` | Print build metadata |
| `hyprmoncfg console session` | Host the desktop and the console in one login session (run by the login manager) |
| `hyprmoncfg console enter` | Close the desktop and start the console session |
| `hyprmoncfg console tv [connector]` | Choose the display the console plays on, or list the candidates |
| `hyprmoncfg console apps list\|add\|remove` | Choose what to close before the desktop goes away |
| `hyprmoncfg console boot [desktop\|console\|last]` | Choose where the machine lands when it starts |
| `hyprmoncfg console leave` | End the console session and bring the desktop back |
| `hyprmoncfg console trigger [on\|off]` | Enter console mode when a controller is switched on |
| `hyprmoncfg console cancel` | Call off an automatic entry that is counting down |
| `hyprmoncfg console status` | Show what console mode is configured to do |
| `hyprmoncfg console doctor` | Check that a console session would work |
| `hyprmoncfg console setup` | Say how to point the login manager at the hosting session |

### Common flags

| Flag | Description |
|------|-------------|
| `--config-dir <path>` | Override the profile storage directory (default: `~/.config/hyprmoncfg`) |
| `--monitors-conf <path>` | Override `HYPRMONCFG_MONITORS_CONF` and the default generated monitor config path |
| `--hypr-config <path>` | Override `HYPRLAND_CONFIG` and the default root config path (`.conf` or `.lua`) |

### Environment variables

Both `hyprmoncfg` and `hyprmoncfgd` use these variables when the corresponding flag is not provided:

| Variable | Description |
|----------|-------------|
| `HYPRMONCFG_MONITORS_CONF` | Generated monitor config path to write and reload |
| `HYPRLAND_CONFIG` | Hyprland root config path used for include verification |

### Apply flags

| Flag | Description |
|------|-------------|
| `--confirm-timeout <seconds>` | Seconds to wait for confirmation before reverting (default: 10) |
| `--confirm-timeout 0` | Disable the revert timer entirely |

## `hyprmoncfgd`

The daemon. Runs in the foreground by default.

### Commands

| Command | Description |
|---------|-------------|
| `hyprmoncfgd` | Start the daemon |
| `hyprmoncfgd version` | Print build metadata |

### Daemon flags

| Flag | Description |
|------|-------------|
| `--config-dir <path>` | Override the profile storage directory |
| `--monitors-conf <path>` | Override `HYPRMONCFG_MONITORS_CONF` and the default generated monitor config path |
| `--hypr-config <path>` | Override `HYPRLAND_CONFIG` and the default root config path (`.conf` or `.lua`) |
| `--profile <name>` | Force a specific profile instead of auto-matching |
| `--debounce <duration>` | Delay before applying after a monitor or lid event (default: 1200ms) |
| `--wake-settle <duration>` | Quiet period after displays wake before reconciling monitor changes (default: 2s) |
| `--poll-interval <duration>` | Polling frequency for monitor fallback checks (default: 5s) |
| `--lid-poll-interval <duration>` | Polling frequency for lid-state fallback checks (default: 1s) |
| `--quiet` | Suppress log output |

## Exit behavior

- CLI commands exit non-zero on Hyprland query failures, invalid layouts, missing profiles, or generated-config verification failures.
- Legacy include-chain failures are reported before writing. For Lua configs, `apply` reloads Hyprland and asks the active Lua state to confirm that the generated monitor file actually ran. It restores the previous file if it did not.
- The daemon exits cleanly on `SIGINT` or `SIGTERM`.
- Only one monitor writer can run per user session. If the daemon owns the writer lock, the TUI and mutating CLI commands use its IPC socket automatically.
