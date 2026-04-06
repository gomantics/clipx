# clipx

LAN clipboard sync for macOS. Copy on one Mac, paste on another. Instantly.

No cloud. No account. No Apple ID. No flaky Universal Clipboard.

## How it works

```
┌─────────────┐     UDP unicast      ┌─────────────┐
│   MacBook    │ ──────────────────► │   iMac       │
│  clipx       │                      │  clipx       │
│  192.168.0.5 │ ◄────────────────── │  192.168.0.6 │
└─────────────┘     UDP unicast      └─────────────┘
```

Each Mac runs `clipx`. When you copy something, it sends the clipboard content directly to all paired peers via UDP. When it receives content from a peer, it writes it to your local clipboard. That's it.

- **UDP unicast** — reliable, fast, no firewall issues with multicast
- **Explicit pairing** — `clipx pair <ip>`, no flaky auto-discovery
- **SHA-256 dedup** — prevents infinite ping-pong loops between nodes
- **10MB max** — large content is automatically chunked into 16KB UDP packets

## Setup

### 1. Install on both Macs

```bash
go install github.com/gomantics/clipx/cmd/clipx@latest
```

Or from source:

```bash
git clone https://github.com/gomantics/clipx.git
cd clipx
make build    # binary at ./clipx-bin
```

### 2. Pair them

On **Mac A** (e.g. 192.168.0.5):

```bash
clipx pair 192.168.0.6    # IP of Mac B
```

On **Mac B** (e.g. 192.168.0.6):

```bash
clipx pair 192.168.0.5    # IP of Mac A
```

### 3. Install and run

On **both Macs**:

```bash
clipx install
```

This does three things:
- Adds a firewall exception so UDP can get through (may ask for sudo)
- Creates a LaunchAgent that starts clipx at login
- Starts clipx immediately

**Done.** Copy on one Mac, paste on the other.

## Disable Apple's Universal Clipboard

Apple's Universal Clipboard will fight with clipx. Turn it off:

**System Settings → General → AirDrop & Handoff → Handoff → Off**

Or from the terminal:

```bash
defaults write ~/Library/Preferences/ByHost/com.apple.coreservices.useractivityd.plist ActivityAdvertisingAllowed -bool no
defaults write ~/Library/Preferences/ByHost/com.apple.coreservices.useractivityd.plist ActivityReceivingAllowed -bool no
```

To re-enable later:

```bash
defaults delete ~/Library/Preferences/ByHost/com.apple.coreservices.useractivityd.plist ActivityAdvertisingAllowed
defaults delete ~/Library/Preferences/ByHost/com.apple.coreservices.useractivityd.plist ActivityReceivingAllowed
```

## Commands

| Command | Description |
|---|---|
| `clipx` | Start the daemon (foreground) |
| `clipx pair <ip>` | Add a peer to sync with |
| `clipx unpair <ip>` | Remove a peer |
| `clipx peers` | List peers and their online status |
| `clipx install` | Install LaunchAgent + firewall exception |
| `clipx uninstall` | Remove the LaunchAgent |
| `clipx status` | Show daemon status and recent logs |
| `clipx update` | Self-update to latest release |
| `clipx version` | Print version |
| `clipx help` | Show help |

## Adding more Macs

Just pair each new Mac with every existing one:

```bash
# On the new Mac:
clipx pair 192.168.0.5
clipx pair 192.168.0.6
clipx install

# On each existing Mac:
clipx pair 192.168.0.7    # IP of new Mac
# restart to pick up new peer:
clipx uninstall && clipx install
```

## Configuration

Peers are stored in `~/.config/clipx/config.json`:

```json
{
  "peers": [
    "192.168.0.5",
    "192.168.0.6"
  ]
}
```

You can edit this file directly if you prefer.

## Ports

| Port | Protocol | Purpose |
|---|---|---|
| 9877 | UDP | Clipboard sync + ping/pong health checks |

One port, UDP only. If you run a firewall, `clipx install` handles it automatically.

## Design

### Protocol

All communication is UDP unicast on port 9877. Three message types:

| Type | Byte | Purpose |
|---|---|---|
| Clipboard | `C` | Carries clipboard content to peers |
| Ping | `P` | Health check request |
| Pong | `A` | Health check response |

Wire format: `[6B magic "CLIPX2"] [1B type] [8B nodeID] [payload]`

Clipboard payload: `[64B SHA-256 hex hash] [clipboard data]`

### Loop prevention

1. Every clipboard write is hashed (SHA-256)
2. When content arrives from a peer, its hash is recorded
3. When the local clipboard watcher detects a change, it checks if the hash matches a recently received peer hash — if so, it skips broadcasting

### Limits

- Max clipboard: **10MB** (content >16KB is automatically chunked)
- Text only (uses `pbcopy`/`pbpaste`)
- macOS only (for now)
- Peers must be on the same LAN

## Troubleshooting

**Clipboard doesn't sync**
- Run `clipx peers` — are peers showing as ● online?
- Run `clipx` in foreground on both machines and watch logs
- Check IPs: `ifconfig en0 | grep inet`

**"address already in use"**
- Another clipx is already running: `pkill clipx` then retry
- Or: `clipx uninstall` then `clipx install`

**Firewall blocking**
- Re-run `clipx install` — it adds the firewall exception
- Or manually: `sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp $(which clipx)`

**Logs**
- LaunchAgent logs: `~/Library/Logs/clipx.log`
- Live tail: `tail -f ~/Library/Logs/clipx.log`

## License

MIT — see [LICENSE](LICENSE).
