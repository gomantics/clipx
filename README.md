# clipx

[![CI](https://github.com/gomantics/clipx/actions/workflows/ci.yml/badge.svg)](https://github.com/gomantics/clipx/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/gomantics/clipx)](https://goreportcard.com/report/github.com/gomantics/clipx)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/gomantics/clipx)](https://github.com/gomantics/clipx/releases/latest)

LAN clipboard sync for macOS. Copy on one Mac, paste on another. Instantly.

No cloud. No account. No Apple ID. No flaky Universal Clipboard.

```
  ┌──────────────────┐                  ┌──────────────────┐
  │  MacBook         │    UDP :9877     │  iMac            │
  │                  │ ──────────────►  │                  │
  │  clipx           │                  │  clipx           │
  │  192.168.0.5     │ ◄──────────────  │  192.168.0.6     │
  └──────────────────┘                  └──────────────────┘
       copy ⌘C                             paste ⌘V
```

## Install

```bash
brew install gomantics/tap/clipx
```

Or with Go:

```bash
go install github.com/gomantics/clipx/cmd/clipx@latest
```

Pre-built binaries for macOS (Intel & Apple Silicon) are on the [releases page](https://github.com/gomantics/clipx/releases/latest).

## Quick start

**1. Pair your Macs**

```bash
# On Mac A (192.168.0.5):
clipx pair 192.168.0.6

# On Mac B (192.168.0.6):
clipx pair 192.168.0.5
```

**2. Install and run** (on both Macs)

```bash
clipx install
```

This adds a firewall exception, creates a LaunchAgent, and starts clipx. **Done.** Copy on one, paste on the other.

> **Tip:** Disable Apple's Universal Clipboard — it will fight with clipx.
> **System Settings → General → AirDrop & Handoff → Handoff → Off**

## Commands

| Command | Description |
|---|---|
| `clipx` | Start the daemon (foreground) |
| `clipx pair <ip>` | Add a peer |
| `clipx unpair <ip>` | Remove a peer |
| `clipx peers` | List peers and their status |
| `clipx install` | Install LaunchAgent + firewall exception |
| `clipx uninstall` | Remove LaunchAgent |
| `clipx status` | Show status and recent logs |
| `clipx update` | Self-update to latest release |
| `clipx version` | Print version |

## Adding more Macs

Pair each new Mac with every existing one:

```bash
# On the new Mac:
clipx pair 192.168.0.5
clipx pair 192.168.0.6
clipx install

# On each existing Mac:
clipx pair 192.168.0.7
clipx uninstall && clipx install
```

## Configuration

Peers live in `~/.config/clipx/config.json`:

```json
{
  "peers": ["192.168.0.5", "192.168.0.6"]
}
```

## How it works

- **UDP unicast** on port 9877 — no multicast, no firewall headaches
- **Explicit pairing** — `clipx pair <ip>`, no flaky auto-discovery
- **SHA-256 dedup** — prevents clipboard ping-pong loops between nodes
- **Chunked transfer** — content >1300 bytes is split into UDP-safe chunks, up to 10 MB
- **Triple send** — small payloads sent 3x for UDP reliability

### Wire format

```
[6B magic "CLIPX2"] [1B type] [8B nodeID] [payload]
```

| Type | Byte | Purpose |
|---|---|---|
| Clipboard | `C` | Clipboard content (single packet) |
| Chunk | `K` | One chunk of large content |
| Ping | `P` | Health check |
| Pong | `A` | Health check response |

## Troubleshooting

**Clipboard doesn't sync** — Run `clipx peers` to check connectivity. Run `clipx` in foreground on both machines to see logs.

**"address already in use"** — Another instance is running. `pkill clipx` or `clipx uninstall && clipx install`.

**Firewall blocking** — Re-run `clipx install` or manually: `sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp $(which clipx)`

**Logs** — `tail -f ~/Library/Logs/clipx.log`

## Contributing

Contributions welcome! See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT — see [LICENSE](LICENSE).
