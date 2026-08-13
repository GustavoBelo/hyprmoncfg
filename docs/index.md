---
layout: home
title: hyprmoncfg
description: Hyprland monitor configuration that actually works.
permalink: /
hero:
  name: hyprmoncfg
  text: A spatial layout editor for Hyprland monitors
  tagline: Arrange once. Save hardware-aware profiles. Switch automatically on monitor hotplug and laptop lid events.
  actions:
    - theme: brand
      text: Install hyprmoncfg
      link: /getting-started/
    - theme: alt
      text: Watch demo
      link: /what-is-hyprmoncfg/#demo
    - theme: alt
      text: GitHub
      link: https://github.com/crmne/hyprmoncfg
    - theme: terminal-trove
      text: >-
        <img class="theme-image light terminal-trove-badge-image" src="/assets/images/terminal-trove-tool-of-the-week-light.svg" alt="Terminal Trove Tool of the Week" width="220" height="58"><img class="theme-image dark terminal-trove-badge-image" src="/assets/images/terminal-trove-tool-of-the-week-dark.svg" alt="Terminal Trove Tool of the Week" width="220" height="58">
      link: https://terminaltrove.com/hyprmoncfg/
  image:
    src: /assets/images/demo.gif
    alt: hyprmoncfg demo
    width: 1400
    height: 800
features:
  - icon: 🖥️
    title: Spatial Layout Editor
    details: Drag monitors on a canvas, then tune display and color settings before previewing and applying the result.
  - icon: 🔌
    title: Hotplug and Lid-Aware Daemon
    details: Save profiles for your real setups. The daemon picks the best match when monitors change or the laptop lid closes.
  - icon: 🔁
    title: Safe Apply with Revert
    details: Every apply writes the generated monitor config atomically, reloads Hyprland, and verifies the result. A 10-second confirmation window means you never get locked out.
  - icon: 🗂️
    title: Workspace Planning
    details: Assign workspaces with sequential, interleave, or manual strategies and apply them together with the monitor layout.
---
