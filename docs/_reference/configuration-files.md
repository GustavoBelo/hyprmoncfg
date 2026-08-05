---
title: Configuration Files
description: Where profiles are stored and what hyprmoncfg writes to Hyprland.
nav_order: 2
---

## Profile storage

Canonical profile JSON files live in:

```
~/.config/hyprmoncfg/profiles/*.json
```

Each profile has a canonical JSON file. The filename is a simplified version of the profile name -- spaces become hyphens, special characters are dropped. For example, a profile named "Home Office" becomes `home-office.json`.

hyprmoncfg also writes generated `home-office.conf` and `home-office.lua` sidecars next to the JSON file. These are fallback exports for people who want to stop using hyprmoncfg but keep the saved layouts as Hyprland config snippets. The JSON remains the source of truth used by hyprmoncfg and `hyprmoncfgd`.

{% include alert.html type="warning" title="Every Profile File Is A Match Candidate" content="`hyprmoncfgd` scans every `*.json` file in this directory. Old backups, temporary experiments, and duplicate layouts are not ignored just because you forgot about them." %}

Override the storage directory with `--config-dir`:

```bash
hyprmoncfg --config-dir /path/to/profiles
hyprmoncfgd --config-dir /path/to/profiles
```

### What's in a profile

Each profile stores:

- **Monitor outputs**: hardware identity (make, model, serial), resolution, refresh rate, scale, position, transform, VRR mode
- **Workspace settings**: strategy, max workspaces, group size, monitor order, explicit rules

Monitors are identified by hardware key (`make|model|serial`), not connector name. This means your profiles survive connector swaps between boots.

## Profile hygiene

If you want predictable daemon behavior, keep this directory curated:

- Create profiles for every real monitor scenario you expect auto-switching to cover
- Keep one profile per real monitor setup you actually want auto-applied
- Delete stale profiles instead of renaming them and leaving them in place
- Store backups somewhere else if you do not want them considered during matching
- Re-save a profile after major hardware changes instead of accumulating near-duplicates

## Hyprland targets

Default legacy apply target:

```
~/.config/hypr/monitors.conf
```

Default legacy root config used for source verification:

```
~/.config/hypr/hyprland.conf
```

On Hyprland 0.55+, if `~/.config/hypr/hyprland.lua` exists, hyprmoncfg switches to Lua mode and uses:

```
~/.config/hypr/monitors.lua
~/.config/hypr/hyprland.lua
```

If Hyprland 0.55+ is still using `hyprland.conf`, hyprmoncfg stays in legacy mode. When `HYPRLAND_CONFIG` is set, hyprmoncfg uses it as the root config path. An explicit `--hypr-config` takes precedence, and paths ending in `.conf` or `.lua` force the matching format.

Override either path:

```bash
hyprmoncfg --monitors-conf /path/to/monitors.conf --hypr-config /path/to/hyprland.conf
hyprmoncfgd --monitors-conf /path/to/monitors.conf --hypr-config /path/to/hyprland.conf
hyprmoncfg --monitors-conf /path/to/monitors.lua --hypr-config /path/to/hyprland.lua
hyprmoncfgd --monitors-conf /path/to/monitors.lua --hypr-config /path/to/hyprland.lua
```

To configure both binaries once, set the equivalent environment variables:

```bash
export HYPRMONCFG_MONITORS_CONF=/path/to/monitors.conf
export HYPRLAND_CONFIG=/path/to/hyprland.conf
```

Explicit flags take precedence. A systemd-managed daemon must receive these variables through the user manager's environment; restart `hyprmoncfgd` after changing them.

## The include-chain check

Before writing anything, hyprmoncfg confirms Hyprland includes the generated monitor file. This catches a surprisingly common problem: a tool writes a config file that Hyprland never reads, so nothing happens and you're left wondering why.

For legacy configs, add `source = ~/.config/hypr/monitors.conf` to `hyprland.conf`. For Lua configs, add `require("monitors")` to `hyprland.lua`. If your files live elsewhere, point hyprmoncfg at them with `--monitors-conf` and `--hypr-config`.

## What gets written

When you apply a profile (via TUI, CLI, or daemon), hyprmoncfg writes the active generated monitor file. Legacy configs get `monitors.conf` with either `monitorv2 { }` blocks (Hyprland 0.50+) or legacy `monitor = ` lines, depending on your Hyprland version. Lua configs get `monitors.lua` with `hl.monitor({ ... })` and `hl.workspace_rule({ ... })` calls.

`monitors.conf` and `monitors.lua` are generated output. hyprmoncfg fully manages and rewrites the active target file on every apply.

Per-profile `*.conf` and `*.lua` files in `~/.config/hyprmoncfg/profiles/` are different: they are exported copies of each saved profile. Applying a profile still regenerates the active target from JSON and the current monitor state so lid handling, duplicate monitor resolution, Hyprland version detection, verification, and rollback keep working.

Do not put unrelated Hyprland settings in that file. Keep blocks like `render`, `cursor`, `misc`, and `env` in other sourced config files.

The file is written atomically (temp file + rename) to prevent partial writes from corrupting your config.

## Portability

Profile JSON files are portable across machines. The daemon uses hardware identity matching to score profiles, so a profile saved on your desktop will work on your laptop if the same monitors are connected. The generated `.conf` and `.lua` sidecars are useful fallback snippets, but JSON is the portable profile format hyprmoncfg reads.

Add `~/.config/hyprmoncfg` to your dotfile manager to share profiles across all your machines. See the [dotfiles guide](/dotfiles/) for details.
