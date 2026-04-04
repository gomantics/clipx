# clipx

LAN clipboard sync for macOS. Copy on one Mac, paste on another. Instantly.

No cloud. No account. No Apple ID. No flaky Universal Clipboard. Just a single binary running quietly on each Mac.

## How it works

```
┌─────────────┐                         ┌─────────────┐
│   MacBook    │   UDP multicast beacon  │   iMac      │
│              │◄───────────────────────►│             │
│  clipx node  │                         │  clipx node  │
│   (a3f2)     │   TCP clipboard sync    │   (b7e1)     │
│              │◄───────────────────────►│             │
└─────────────┘        LAN only          └─────────────┘
```

Each Mac runs the same `clipx` binary. It:

1. **Watches** your clipboard every 500ms for changes
2. **Discovers** other clipx nodes on the LAN via UDP multicast (no config needed)
3. **Sends** new clipboard content to all discovered peers over TCP
4. **Receives** clipboard content from peers and writes it to your local clipboard

Nodes find each other automatically using link-local multicast (`224.0.0.177`), which never leaves your local network. Content is transferred directly peer-to-peer over TCP. No server, no relay, no internet required.

A SHA-256 hash of every clipboard write prevents infinite ping-pong loops between nodes.

## Install

### Using Go

```bash
go install github.com/gomantics/clipx/cmd/clipx@latest
```

### From source

```bash
git clone https://github.com/gomantics/clipx.git
cd clipx
make build
# binary is at ./clipx
```

### From releases

Download the latest binary from [GitHub Releases](https://github.com/gomantics/clipx/releases) for your architecture (Apple Silicon or Intel).

## Quick start

Run on **every Mac** you want to sync:

```bash
clipx
```

That's it. Copy something on one Mac, it appears on the other. You'll see:

```
[clipx] 2025/04/05 02:40:01 starting clipx v0.1.0 node=a3f2e1b7
[clipx] 2025/04/05 02:40:03 discovered peer b7e1c4d2 at 192.168.1.42:9878
[clipx] 2025/04/05 02:40:15 → broadcasting (42 bytes): https://github.com/gomantics/clipx
[clipx] 2025/04/05 02:40:15 ← recv from a3f2e1b7 (42 bytes): https://github.com/gomantics/clipx
```

## Run at login (recommended)

Install clipx as a macOS LaunchAgent so it starts silently at login and runs in the background:

```bash
clipx install
```

```
✓ clipx installed and started as LaunchAgent
  plist: ~/Library/LaunchAgents/com.gomantics.clipx.plist
  logs:  ~/Library/Logs/clipx.log
  binary: /usr/local/bin/clipx

clipx will start automatically at login.
Run 'clipx uninstall' to remove.
```

Check status anytime:

```bash
clipx status
```

Remove the LaunchAgent:

```bash
clipx uninstall
```

## Disable Apple's Universal Clipboard

Apple's Universal Clipboard will fight with clipx. Turn it off:

1. **On each Mac**, go to **System Settings → General → AirDrop & Handoff**
2. Toggle **Handoff** to **off**

> **Note:** Disabling Handoff also disables Universal Clipboard. If you use Handoff for other things (continuing apps between devices), you can leave it on — clipx will still work, but you might get occasional double-pastes from both systems syncing simultaneously.

Alternatively, from the terminal:

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
|---------|-------------|
| `clipx` | Start the clipboard sync daemon |
| `clipx install` | Install as macOS LaunchAgent (auto-start at login) |
| `clipx uninstall` | Remove the LaunchAgent |
| `clipx status` | Show running status and recent logs |
| `clipx version` | Print version |
| `clipx help` | Show help |

## Design

### Auto-discovery

Nodes announce themselves every 2 seconds via UDP multicast on `224.0.0.177:9877`. This is a link-local multicast address — packets **never leave your LAN**, not even to other subnets. When a node hears a beacon from a new peer, it logs the discovery and starts syncing.

Peers that stop sending beacons are removed after 10 seconds.

### Clipboard sync protocol

When a clipboard change is detected:

1. Compute SHA-256 hash of the content
2. Compare against last known hash (skip if unchanged)
3. Check if this hash was recently received from a peer (skip to prevent loops)
4. Send to all known peers via TCP using a simple binary protocol:
   - `CLIPX1` (6 bytes magic)
   - Node ID (8 bytes)
   - Content length (4 bytes, big-endian)
   - Content (raw bytes)

### Limits

- Max clipboard size: **10 MB** (larger content is silently skipped)
- Text only (uses `pbcopy`/`pbpaste` under the hood)
- Requires all Macs on the **same LAN/subnet**
- Multicast must not be blocked by your router (it isn't on any normal home/office network)

### Ports

| Port | Protocol | Purpose |
|------|----------|---------|
| 9877 | UDP | Multicast discovery beacons |
| 9878 | TCP | Clipboard content transfer |

If you run a firewall, allow these ports for local network traffic.

## Troubleshooting

**Nodes don't discover each other**
- Ensure both Macs are on the same Wi-Fi network / subnet
- Check that no firewall is blocking UDP 9877 or TCP 9878
- Some corporate networks block multicast — try a simple network

**Clipboard doesn't sync**
- Run `clipx` in the foreground on both machines and watch the logs
- Make sure you're copying text (images/files are not synced yet)

**"tcp listen: address already in use"**
- Another clipx instance is already running
- Check with: `lsof -i :9878`
- Kill it: `pkill clipx` or `clipx uninstall && clipx install`

**Logs location**
- When running as LaunchAgent: `~/Library/Logs/clipx.log`
- Tail live: `tail -f ~/Library/Logs/clipx.log`

## License

MIT — see [LICENSE](LICENSE).
