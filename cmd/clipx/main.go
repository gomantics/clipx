package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

var version = "dev"

func getVersion() string {
	if version != "dev" && version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

const (
	multicastGroup = "239.77.77.77"
	multicastPort  = 9877
	multicastAddr  = "239.77.77.77:9877"
	beaconInterval = 2 * time.Second
	pollInterval   = 500 * time.Millisecond
	maxClipSize    = 64 * 1024 // 64KB — safe for UDP on LAN

	// message types
	msgBeacon    = "B"
	msgClipboard = "C"
)

// wire format (all UDP multicast):
//   [1 byte type] [8 bytes nodeID] [payload...]
//
// beacon payload:  (empty — presence is enough)
// clipboard payload: [32 bytes sha256 hex hash] [clipboard data]

type Node struct {
	id           string
	mu           sync.Mutex
	lastHash     string
	peerHashes   map[string]time.Time
	peerHashesMu sync.Mutex
	peers        map[string]time.Time // nodeID -> lastSeen
	peersMu      sync.Mutex
	sender       *ipv4.PacketConn
	senderConn   net.PacketConn
	senderMu     sync.Mutex
	dst          *net.UDPAddr
	logger       *log.Logger
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("clipx %s\n", getVersion())
			return
		case "install":
			installLaunchAgent()
			return
		case "uninstall":
			uninstallLaunchAgent()
			return
		case "status":
			showStatus()
			return
		case "update", "self-update":
			selfUpdate()
			return
		case "help", "--help", "-h":
			printUsage()
			return
		}
	}

	logger := log.New(os.Stdout, "[clipx] ", log.LstdFlags|log.Lmsgprefix)

	dst, _ := net.ResolveUDPAddr("udp4", multicastAddr)

	node := &Node{
		id:         uuid.New().String()[:8],
		peerHashes: make(map[string]time.Time),
		peers:      make(map[string]time.Time),
		dst:        dst,
		logger:     logger,
	}

	logger.Printf("starting clipx %s node=%s", getVersion(), node.id)

	// initialize lastHash with current clipboard content
	if content, err := readClipboard(); err == nil && len(content) > 0 {
		node.lastHash = hashContent(content)
	}

	// setup sender (multicast out)
	node.setupSender()

	// start listener, beacon, clipboard watcher
	go node.listenMulticast()
	go node.sendBeacons()
	go node.watchClipboard()
	go node.maintenance()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	logger.Println("shutting down")
}

func printUsage() {
	fmt.Printf(`clipx %s — LAN clipboard sync for macOS

Usage:
  clipx              Start the clipboard sync daemon
  clipx install      Install as macOS LaunchAgent (starts at login)
  clipx uninstall    Remove the LaunchAgent
  clipx status       Show running status and peers
  clipx update       Self-update to the latest release
  clipx version      Print version
  clipx help         Show this help
`, getVersion())
}

// --- Multicast sender ---

func (n *Node) setupSender() {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		n.logger.Fatalf("sender listen: %v", err)
	}
	n.senderConn = conn
	n.sender = ipv4.NewPacketConn(conn)
	n.sender.SetMulticastTTL(2)
	n.sender.SetMulticastLoopback(true)
	n.bindSenderInterface()
}

func (n *Node) bindSenderInterface() {
	if iface := findMulticastInterface(); iface != nil {
		if err := n.sender.SetMulticastInterface(iface); err != nil {
			n.logger.Printf("warning: bind sender to %s: %v", iface.Name, err)
		} else {
			n.logger.Printf("sender bound to %s", iface.Name)
		}
	}
}

func (n *Node) multicastSend(data []byte) error {
	n.senderMu.Lock()
	defer n.senderMu.Unlock()
	_, err := n.senderConn.WriteTo(data, n.dst)
	return err
}

// --- Multicast listener ---

func (n *Node) listenMulticast() {
	group := net.ParseIP(multicastGroup)

	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var opErr error
			c.Control(func(fd uintptr) {
				opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
				if opErr != nil {
					return
				}
				opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
			})
			return opErr
		},
	}

	conn, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf(":%d", multicastPort))
	if err != nil {
		n.logger.Fatalf("listen: %v", err)
	}
	defer conn.Close()

	p := ipv4.NewPacketConn(conn)

	joinAll := func() int {
		joined := 0
		for _, iface := range multicastInterfaces() {
			p.LeaveGroup(&iface, &net.UDPAddr{IP: group})
			if err := p.JoinGroup(&iface, &net.UDPAddr{IP: group}); err != nil {
				continue
			}
			n.logger.Printf("listening on %s", iface.Name)
			joined++
		}
		return joined
	}

	if joinAll() == 0 {
		n.logger.Fatal("could not join multicast on any interface")
	}

	// periodic re-join
	go func() {
		for {
			time.Sleep(30 * time.Second)
			joinAll()
		}
	}()

	p.SetControlMessage(ipv4.FlagDst, true)

	buf := make([]byte, 70*1024) // 64KB data + header overhead

	for {
		nBytes, cm, _, err := p.ReadFrom(buf)
		if err != nil {
			n.logger.Printf("recv error: %v", err)
			continue
		}

		if cm != nil && cm.Dst != nil && !cm.Dst.Equal(group) {
			continue
		}

		if nBytes < 9 { // 1 type + 8 nodeID minimum
			continue
		}

		msgType := string(buf[0:1])
		nodeID := string(buf[1:9])

		if nodeID == fmt.Sprintf("%-8s", n.id) {
			continue // ignore self
		}

		senderID := strings.TrimSpace(nodeID)

		switch msgType {
		case msgBeacon:
			n.peersMu.Lock()
			_, found := n.peers[senderID]
			if !found {
				n.logger.Printf("discovered peer %s", senderID)
			}
			n.peers[senderID] = time.Now()
			n.peersMu.Unlock()

		case msgClipboard:
			if nBytes < 9+64 { // 1 + 8 + 64 hash hex
				continue
			}
			hash := string(buf[9:73])
			data := make([]byte, nBytes-73)
			copy(data, buf[73:nBytes])

			n.mu.Lock()
			if hash == n.lastHash {
				n.mu.Unlock()
				continue
			}
			n.lastHash = hash
			n.mu.Unlock()

			n.peerHashesMu.Lock()
			n.peerHashes[hash] = time.Now()
			n.peerHashesMu.Unlock()

			if err := writeClipboard(data); err != nil {
				n.logger.Printf("clipboard write error: %v", err)
				continue
			}

			preview := string(data)
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			preview = strings.ReplaceAll(preview, "\n", "⏎")
			n.logger.Printf("← from %s (%d bytes): %s", senderID, len(data), preview)

			// also mark this peer as seen
			n.peersMu.Lock()
			n.peers[senderID] = time.Now()
			n.peersMu.Unlock()
		}
	}
}

// --- Beacon sender ---

func (n *Node) sendBeacons() {
	// build beacon: type + nodeID
	nodeID := fmt.Sprintf("%-8s", n.id)
	msg := []byte(msgBeacon + nodeID)

	for {
		if err := n.multicastSend(msg); err != nil {
			n.logger.Printf("beacon send error: %v", err)
			n.bindSenderInterface()
		}
		time.Sleep(beaconInterval)
	}
}

// --- Clipboard watcher ---

func (n *Node) watchClipboard() {
	for {
		time.Sleep(pollInterval)

		data, err := readClipboard()
		if err != nil || len(data) == 0 {
			continue
		}

		if len(data) > maxClipSize {
			continue
		}

		hash := hashContent(data)

		n.mu.Lock()
		if hash == n.lastHash {
			n.mu.Unlock()
			continue
		}
		n.lastHash = hash
		n.mu.Unlock()

		// don't broadcast content we just received from a peer
		n.peerHashesMu.Lock()
		if _, fromPeer := n.peerHashes[hash]; fromPeer {
			n.peerHashesMu.Unlock()
			continue
		}
		n.peerHashesMu.Unlock()

		// check we have at least one peer
		n.peersMu.Lock()
		peerCount := len(n.peers)
		n.peersMu.Unlock()

		preview := string(data)
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", "⏎")

		if peerCount == 0 {
			n.logger.Printf("→ skipped (%d bytes, no peers): %s", len(data), preview)
			continue
		}

		n.logger.Printf("→ sending (%d bytes, %d peers): %s", len(data), peerCount, preview)

		// build clipboard message: type + nodeID + hash + data
		nodeID := fmt.Sprintf("%-8s", n.id)
		hashHex := hashContent(data) // 64 hex chars
		msg := make([]byte, 0, 1+8+64+len(data))
		msg = append(msg, []byte(msgClipboard)...)
		msg = append(msg, []byte(nodeID)...)
		msg = append(msg, []byte(hashHex)...)
		msg = append(msg, data...)

		// send via multicast — all peers get it at once
		if err := n.multicastSend(msg); err != nil {
			n.logger.Printf("send error: %v", err)
			n.bindSenderInterface()
		}
	}
}

// --- Maintenance ---

func (n *Node) maintenance() {
	for {
		time.Sleep(10 * time.Second)

		// cleanup stale peers
		n.peersMu.Lock()
		for id, lastSeen := range n.peers {
			if time.Since(lastSeen) > 10*time.Second {
				n.logger.Printf("peer %s timed out", id)
				delete(n.peers, id)
			}
		}
		n.peersMu.Unlock()

		// cleanup old peer hashes
		n.peerHashesMu.Lock()
		for h, t := range n.peerHashes {
			if time.Since(t) > 30*time.Second {
				delete(n.peerHashes, h)
			}
		}
		n.peerHashesMu.Unlock()

		// periodic sender rebind
		n.bindSenderInterface()
	}
}

// --- Clipboard (macOS native) ---

func readClipboard() ([]byte, error) {
	cmd := exec.Command("pbpaste")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func writeClipboard(data []byte) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(string(data))
	return cmd.Run()
}

func hashContent(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// --- Network helpers ---

func findMulticastInterface() *net.Interface {
	for _, iface := range multicastInterfaces() {
		return &iface
	}
	return nil
}

func multicastInterfaces() []net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var result []net.Interface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				result = append(result, iface)
				break
			}
		}
	}
	return result
}

// --- LaunchAgent ---

const launchAgentLabel = "com.gomantics.clipx"

var launchAgentPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
    <key>ProcessType</key>
    <string>Background</string>
</dict>
</plist>
`

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

func logFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "clipx.log")
}

func installLaunchAgent() {
	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine binary path: %v\n", err)
		os.Exit(1)
	}
	binPath, _ = filepath.EvalSymlinks(binPath)

	plistPath := launchAgentPath()
	logPath := logFilePath()

	tmpl, _ := template.New("plist").Parse(launchAgentPlist)

	f, err := os.Create(plistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot create %s: %v\n", plistPath, err)
		os.Exit(1)
	}
	defer f.Close()

	data := struct {
		Label, BinaryPath, LogPath string
	}{launchAgentLabel, binPath, logPath}

	if err := tmpl.Execute(f, data); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot write plist: %v\n", err)
		os.Exit(1)
	}

	exec.Command("launchctl", "unload", plistPath).Run()

	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: launchctl load failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ clipx installed and started as LaunchAgent")
	fmt.Printf("  plist:  %s\n", plistPath)
	fmt.Printf("  logs:   %s\n", logPath)
	fmt.Printf("  binary: %s\n", binPath)
	fmt.Println("")
	fmt.Println("clipx will start automatically at login.")
	fmt.Println("Run 'clipx uninstall' to remove.")
}

func uninstallLaunchAgent() {
	plistPath := launchAgentPath()
	exec.Command("launchctl", "unload", plistPath).Run()

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: cannot remove %s: %v\n", plistPath, err)
		os.Exit(1)
	}

	fmt.Println("✓ clipx LaunchAgent removed")
	fmt.Println("clipx will no longer start at login.")
}

func showStatus() {
	out, err := exec.Command("launchctl", "list", launchAgentLabel).Output()
	if err != nil {
		fmt.Println("● clipx is not running as a LaunchAgent")
	} else {
		fmt.Println("● clipx is running as a LaunchAgent")
		fmt.Println(string(out))
	}

	logPath := logFilePath()
	fmt.Printf("  logs: %s\n", logPath)
	fmt.Println("")
	fmt.Println("Recent log:")
	tail, _ := exec.Command("tail", "-20", logPath).Output()
	if len(tail) > 0 {
		fmt.Print(string(tail))
	} else {
		fmt.Println("  (no logs yet)")
	}
}

// --- Self-update ---

func selfUpdate() {
	current := getVersion()
	fmt.Printf("current version: %s\n", current)
	fmt.Println("updating via go install...")

	cmd := exec.Command("go", "install", "github.com/gomantics/clipx/cmd/clipx@latest")
	cmd.Env = append(os.Environ(),
		"GONOSUMDB=github.com/gomantics/clipx",
		"GONOSUMCHECK=github.com/gomantics/clipx",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: go install failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ clipx updated")

	plistPath := launchAgentPath()
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Println("restarting LaunchAgent...")
		exec.Command("launchctl", "unload", plistPath).Run()
		exec.Command("launchctl", "load", plistPath).Run()
		fmt.Println("✓ LaunchAgent restarted")
	}
}

