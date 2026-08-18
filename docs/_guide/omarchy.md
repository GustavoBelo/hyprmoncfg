---
title: Omarchy Quattro Panel
description: Create multi-monitor layouts for Hyprland in a visual editor and switch them automatically on hotplug and lid events.
nav_order: 4
---

## Displays, handled

The [hyprmoncfg: Multi-Monitor Manager for Omarchy](https://github.com/crmne/omarchy-hyprmoncfg) plugin lets you create multi-monitor layouts for Hyprland in a visual editor and switch them automatically on hotplug and lid events. Open the panel to see the layout that is live right now, the profile that matched it, and whether hyprmoncfg is managing your displays.

![hyprmoncfg panel for Omarchy Quattro](https://raw.githubusercontent.com/crmne/omarchy-hyprmoncfg/main/preview.png)

If you find the panel useful, I'd appreciate a [star on GitHub](https://github.com/crmne/omarchy-hyprmoncfg). It helps get the word out.

Turn management on and hyprmoncfg switches profiles automatically on monitor hotplug and laptop lid events. Turn it off and display ownership goes cleanly back to Omarchy. **Layout and settings** opens the full spatial editor in Omarchy's centered TUI window.

## Install the panel

Get it from the [Omarchy Plugins marketplace](https://omarchyplugins.com/plugin.html?id=crmne.hyprmoncfg), or install it directly:

```bash
omarchy plugin add https://github.com/crmne/omarchy-hyprmoncfg.git --enable
```

If hyprmoncfg is already installed, the panel connects to its daemon over the local IPC socket and updates as monitor state changes.

If it is missing, open the panel and choose **Install hyprmoncfg**. The panel uses Omarchy's normal presented package flow to install the stable AUR package, enables and starts `hyprmoncfgd.service`, and then opens the layout editor. Arrange your monitors, press `s`, and save your first profile. The panel refreshes as soon as it is ready.

## What the panel controls

- **Managed by hyprmoncfg** enables or disables the user daemon
- **Layout and settings** opens the spatial TUI
- **Profile** shows the active hardware-aware profile and whether it was selected automatically

The panel is a desktop surface for the same daemon and IPC protocol used by the TUI and CLI. It does not maintain another copy of monitor state or write Hyprland configuration on its own.

## When Omarchy still moves your displays

Omarchy manages monitors too, and two managers can disagree. The daemon stops Omarchy's monitor watcher while it owns your displays, but Omarchy also writes a clamshell rule and reloads Hyprland from lid and wake events.

Whether that rule wins comes down to load order. Omarchy's `hyprland.lua` loads its dynamic toggles last, so a config that reads hyprmoncfg's monitors earlier hands Omarchy the final word on every reload:

```bash
hyprmoncfg doctor
```

If it reports a problem, `hyprmoncfg doctor --fix` moves the `require("hypr.monitors")` line below `require("default.hypr.toggles")` and saves your previous config next to it. Run `hyprctl reload` afterwards. Adding a second require later in the file does not work: Lua caches modules, so the repeat call does nothing.

The daemon also puts a profile back when something outside hyprmoncfg moves the displays, including a profile you picked by hand.

## Remove it

```bash
omarchy plugin remove crmne.hyprmoncfg
```

Removing the panel leaves hyprmoncfg and your saved profiles installed. Turn off **Managed by hyprmoncfg** first if you also want Omarchy to resume display management.
