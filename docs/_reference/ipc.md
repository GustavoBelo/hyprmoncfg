---
title: IPC Protocol
description: Versioned Unix-socket protocol for controlling hyprmoncfgd.
nav_order: 3
---

## Overview

`hyprmoncfgd` exposes a newline-delimited JSON protocol on:

```text
$XDG_RUNTIME_DIR/hyprmoncfgd.sock
```

The socket is mode `0600`. Protocol version 1 is designed for the bundled TUI and CLI as well as local desktop integrations such as an Omarchy bar panel.

When the socket exists, the daemon is the canonical writer. Clients should not edit profiles or the active generated monitor file alongside it. The bundled TUI and CLI fall back to the same core engine only when no daemon owns the session writer lock.

## Envelopes

Every message is one JSON object followed by a newline. A request looks like:

```json
{"type":"request","protocol_version":1,"id":"1","method":"status"}
```

The matching response repeats the client-generated ID:

```json
{"type":"response","protocol_version":1,"id":"1","result":{}}
```

Errors replace `result` with an object containing a stable `code`, a human-readable `message`, and optional `data`.

## Methods

| Method | Parameters | Result |
|---|---|---|
| `status` | none | Status document |
| `subscribe` | none | Current status document, followed by `status` events |
| `preview` | `profile_name` or a full `profile`; optional `timeout_seconds` | Transaction |
| `confirm` | `transaction_id` | none |
| `revert` | `transaction_id` | none |
| `save` | full `profile` | none |
| `delete` | `name` | none |

A transaction contains an opaque `id`, the effective profile, and an RFC 3339 `deadline`.

## Safe preview lifecycle

Only one preview can be active at a time. It belongs to the connection that created it and is reverted when any of these happens:

- the client calls `revert`
- the deadline expires
- the IPC connection closes before confirmation
- the daemon shuts down

After `confirm`, the selected profile remains active until the next monitor hotplug or lid change. A late `confirm` or `revert` returns the `transaction_unavailable` error code.

## Status events

After `subscribe`, the daemon pushes a fresh status document whenever monitor or profile state changes:

```json
{"type":"event","protocol_version":1,"event":"status","data":{}}
```

The document schema is versioned independently with `schema_version`. Each profile summary includes `connected_enabled_outputs` and `match_score`; integrations should only offer a profile for manual selection when `connected_enabled_outputs` is greater than zero. This guarantees that applying the profile leaves at least one currently connected output enabled. `connected_enabled_outputs` can also be shown against `enabled_outputs` to explain how much of the profile is currently available.

Monitor summaries include the connector, make, model, active mode, physical and logical dimensions, position, scale, transform, internal/focused flags, and enabled state. Integrations can therefore render the same monitor identity and layout information as the TUI without querying Hyprland separately. `hyprmoncfg status --json` prints the same shape without requiring clients to speak the socket protocol.
