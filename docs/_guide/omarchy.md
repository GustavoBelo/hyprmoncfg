---
title: Omarchy Quattro Panel
description: Put your live layout, active profile, and automatic monitor switching in the Omarchy bar.
nav_order: 4
---

## Displays, handled

The [hyprmoncfg: Monitor Manager for Omarchy](https://omarchyplugins.com/plugin.html?id=crmne.hyprmoncfg) plugin brings hyprmoncfg into the Omarchy Quattro bar. Open it to see the layout that is live right now, the profile that matched it, and whether hyprmoncfg is managing your displays.

![hyprmoncfg panel for Omarchy Quattro](https://raw.githubusercontent.com/crmne/omarchy-hyprmoncfg/main/preview.png)

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

## Remove it

```bash
omarchy plugin remove crmne.hyprmoncfg
```

Removing the panel leaves hyprmoncfg and your saved profiles installed. Turn off **Managed by hyprmoncfg** first if you also want Omarchy to resume display management.
