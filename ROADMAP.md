# Roadmap

What's next for clipx. Roughly ordered by priority.

## v0.6 — Encryption

Right now clipboard content flies across your LAN in plaintext. Fine for a home network, sketchy for a shared office.

- [ ] AES-256-GCM encryption of all clipboard payloads
- [ ] Shared secret generated on first `clipx pair`, exchanged out-of-band (displayed in terminal, user copies it)
- [ ] Zero config overhead — encryption is always on once paired

## v0.7 — Image support

Currently text only (`pbcopy`/`pbpaste`). macOS has richer clipboard APIs.

- [ ] Detect clipboard content type (text, image, file path)
- [ ] Image sync using `osascript` or native clipboard APIs via cgo
- [ ] Respect size limits — images can be large, chunk transfer already handles up to 10 MB

## v0.8 — Smarter pairing

Manual IP entry works but is friction.

- [ ] `clipx pair --scan` — scan the local subnet for other clipx instances
- [ ] mDNS/Bonjour discovery as an alternative to IP-based pairing
- [ ] Pairing confirmation — peer shows a prompt to accept/reject

## v0.9 — Linux support

The protocol is platform-agnostic, only the clipboard is macOS-specific.

- [ ] Abstract clipboard behind build tags (`clipboard_darwin.go`, `clipboard_linux.go`)
- [ ] Linux: `xclip`/`xsel` for X11, `wl-copy`/`wl-paste` for Wayland
- [ ] CI testing on Linux

## v1.0 — Stable release

- [ ] All of the above shipped and stable
- [ ] Homebrew core submission (not just a tap)
- [ ] Man page (`clipx.1`)
- [ ] Proper semantic versioning contract — no breaking changes after v1

## Maybe later

Things that might be worth doing but aren't committed to.

- **File sync** — drag a file on one Mac, paste on another. Needs a different transport (TCP for reliability).
- **Clipboard history** — keep the last N items, `clipx history` to browse.
- **Menu bar app** — native macOS status bar icon showing connection status. Probably a separate repo.
- **Windows support** — `clip.exe` / PowerShell clipboard. Low priority unless there's demand.
- **Tailscale/WireGuard awareness** — detect if peers are on a VPN and skip encryption (already encrypted at the tunnel level).
