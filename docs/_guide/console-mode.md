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

It warns you and waits ten seconds before handing over. Ending the desktop
session closes what was open in it, the same way logging out does. To come
back, use Big Picture's own **Steam → Power → Switch to Desktop**.

There is also a **Console Mode** entry in your application launcher.

### Changing your mind

The warning is a notification that says to click it, and clicking anywhere on it
calls the entry off. The desktop stays, and the notification says so.

Clicking the body is the answer that always works. Notification servers that
draw action buttons show a **Cancel** button as well, but most draw none --
mako, dunst and Omarchy's own quickshell among them -- and nothing in the
protocol lets a program find out which kind it is talking to. So the message
asks for the click, and the button is a bonus where there is one.

That matters most for the launcher entry, which is the one you can click by
mistake. Every way in gets the same notification, so there is nowhere you can
start this from and be unable to stop it.

If your notification server cannot take an answer back, the notification says to
run this instead:

```bash
hyprmoncfg console cancel
```

which works during any countdown, from anywhere.

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
your desktop, it announces itself and waits twenty seconds -- twice as long as
an entry you asked for, since a controller switching itself on is as often an
accident as an intention. Clicking the notification calls it off, and so does
switching the controller off again, or `hyprmoncfg console cancel`.

It is off by default.

## Where the machine starts

By default it starts at the desktop, exactly as it did before console mode
existed, and the console is something you ask for.

```bash
hyprmoncfg console boot last      # wherever the last session ended
hyprmoncfg console boot console   # always the console, like a games machine
hyprmoncfg console boot desktop   # always the desktop (the default)
```

`last` is the one to pick if you want it to feel like a console without giving
up the desktop: shut down playing and it boots playing, shut down working and it
boots working.

### What still asks for a password

Starting in the console does not skip anything your machine already asks for on
the way up, and it cannot:

- **A disk passphrase**, if the disk is encrypted. It comes long before any of
  this, needs a keyboard, and appears wherever your firmware puts it.
- **The login manager**, unless it logs you in automatically. Its greeter comes
  up on its own display -- normally the desk monitor -- and only after you have
  typed the password does the machine hand over to the TV. A console that stops
  at a desk to ask for a password is not really a console, so if you want the
  machine to come up on the TV, turn autologin on in your login manager.

`hyprmoncfg console doctor` says so when it can tell. It reads SDDM's
configuration; for other login managers it says it cannot tell rather than
guessing.

A Steam Deck has neither of these, which is why it feels the way it does.

In every case, if the console cannot start the desktop comes up instead, and two
sessions that end immediately hand the machine back to the login manager rather
than looping.

Booting into the console waits for the TV to present itself first. gamescope
enumerates connectors once and never looks again, so starting it before the
displays are ready leaves it running with nothing selected -- a black screen that
only a physical replug recovers from. The wait is up to twenty seconds; after
that it starts anyway, because a television that is switched off never becomes
ready and refusing to start would be worse.

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

**A controller is not required to enter.** The controller trigger is one way in
among several, and it is off by default. Booting into the console, the launcher
entry, the panel button and `console enter` all work with no controller
connected -- Big Picture is navigable with a keyboard and mouse.

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
