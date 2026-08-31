---
title: Console Mode
description: Hand the machine to Steam's gamescope session on the TV, and get the desktop back afterwards.
nav_order: 7
---

## What it does

Console Mode closes your desktop session and starts Steam's gamescope session on
the TV instead. When you leave Big Picture, your desktop comes back, with sound
where it was.

It is not a window on your desktop. gamescope becomes the compositor and owns
the display directly, which is where per-game HDR, VRR, FSR, tearing and the
frame limiter come from -- Steam only offers those controls when the session it
is running in declares them, and only a real gamescope session does.

The price is that your desktop is gone while you play. That is the trade: a real
console instead of a game in a window.

## What you need

- **gamescope**, and a **gamescope session** package. On Arch and CachyOS that is
  `gamescope-session-cachyos`; other distributions package the same thing under
  other names. Console Mode looks for a session entry that declares
  `DesktopNames=gamescope`, never for a package name.
- **Steam**, logged in.
- The **hosting session** installed once -- see below.

`hyprmoncfg console doctor` checks all of it and says what is missing.

## Setting it up

Console Mode works by hosting: your login manager starts one session that runs
your desktop compositor, and swaps it for the gamescope session when you ask.
Your login manager never sees the session end, so switching needs no password
and no greeter.

That means the login manager has to start `hyprmoncfg console session` instead
of your compositor directly. This is the one step that needs root, and it is
done once:

```bash
hyprmoncfg console tv          # list displays
hyprmoncfg console tv HDMI-A-1 # choose the TV
hyprmoncfg console setup       # prints exactly what to change
```

`console setup` never edits a system file. It detects your login manager, writes
a session entry into your config directory, and prints the change to make and
how to undo it. Getting this wrong leaves a machine that will not present a
desktop, so the decision stays with you.

Log out and back in, then:

```bash
hyprmoncfg console doctor
```

## Using it

```bash
hyprmoncfg console enter
```

It warns you, waits five seconds so you can press Ctrl-C, closes the
applications you have tracked, and hands over. To come back, use Big Picture's
own **Steam → Power → Switch to Desktop**.

There is also a **Console Mode** entry in your application launcher.

### A keyboard shortcut

Add this to your Hyprland config:

```
bind = SUPER SHIFT, G, exec, hyprmoncfg console enter
```

### On a controller

```bash
hyprmoncfg console trigger on
```

Switching a controller on then starts a console session. Because that closes
your desktop, it announces itself and waits twenty seconds; switching the
controller off again calls it off, and so does `hyprmoncfg console cancel`.

It is off by default.

## Closing applications first

Entering takes the desktop down with everything on it. Anything you list is
asked to close gracefully first, while there is still a compositor for it to put
a save dialog on:

```bash
hyprmoncfg console apps list          # what is running, and what is tracked
hyprmoncfg console apps add obsidian
```

Matching is exact -- a window class or a `/proc` comm, never a title substring --
which is why the list is picked from what is running rather than typed.

## Sound

The TV's audio output is found through the EDID. Every HDMI sink on a graphics
card is described as the card itself, so picking one by name is a coin flip;
ALSA publishes each HDMI pin's monitor name, and that is the only thing that
reliably ties a display to a sound device. Sound moves to the TV on the way in
and back on the way out.

## Things worth knowing

**Resolution and refresh are not set here.** gamescope takes the connector's
preferred mode, and Steam changes it per game from its own settings. A
resolution recorded by hyprmoncfg would be a second, disagreeing source of
truth.

**Bluetooth is Steam's.** In a gamescope session Steam manages the Bluetooth
adapter itself and applies its own stored setting -- which starts off. If your
controller will not connect, turn Bluetooth on in Steam's Quick Access Menu
once; Steam remembers.

**The Quick Access Menu** opens with the Steam button plus **A** (Cross on a
PlayStation pad). Steam's own chord configuration leaves B/Circle unbound, so
that combination opens the main menu and immediately dismisses it.

**Only SDDM has been tested.** The hosting session is designed to work with any
login manager, or none, but greetd, ly, GDM and tty logins have not been tried.
`console setup` prints the right instruction for what it detects and says when
it has not been tested.
