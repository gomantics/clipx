package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
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

	// needed for SO_REUSEADDR/SO_REUSEPORT on the multicast listener
	"golang.org/x/sys/unix"
)

func getVersion() string {
	// ldflags -X main.version takes priority (goreleaser sets this)
	if version != "dev" && version != "" {
		return version
	}
	// fallback: go install embeds module version in build info
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

const (
	multicastAddr  = "239.77.77.77:9877"
	tcpPort        = 9878
	beaconInterval = 2 * time.Second
	pollInterval   = 500 * time.Millisecond
	maxClipSize    = 10 * 1024 * 1024 // 10MB
	magicHeader    = "CLIPX1"
)

type Node struct {
	id       string
	mu       sync.Mutex
	lastHash string
	// tracks hashes we received from peers so we don't re-broadcast them
	peerHashes   map[string]time.Time
	peerHashesMu sync.Mutex
	peers        map[string]peerInfo
	peersMu      sync.Mutex
	logger       *log.Logger
}

type peerInfo struct {
	addr     string
	lastSeen time.Time
}

var version = "dev"

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

	node := &Node{
		id:         uuid.New().String()[:8],
		peerHashes: make(map[string]time.Time),
		peers:      make(map[string]peerInfo),
		logger:     logger,
	}

	logger.Printf("starting clipx %s node=%s", getVersion(), node.id)

	// initialize lastHash with current clipboard content
	if content, err := readClipboard(); err == nil && len(content) > 0 {
		node.lastHash = hashContent(content)
	}

	go node.startBeacon()
	go node.listenBeacon()
	go node.listenTCP()
	go node.watchClipboard()
	go node.cleanupPeers()

	// graceful shutdown
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
	// find our own binary path
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

	// unload if already loaded (ignore errors)
	exec.Command("launchctl", "unload", plistPath).Run()

	// load the agent
	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: launchctl load failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ clipx installed and started as LaunchAgent")
	fmt.Printf("  plist: %s\n", plistPath)
	fmt.Printf("  logs:  %s\n", logPath)
	fmt.Printf("  binary: %s\n", binPath)
	fmt.Println("")
	fmt.Println("clipx will start automatically at login.")
	fmt.Println("Run 'clipx uninstall' to remove.")
}

func uninstallLaunchAgent() {
	plistPath := launchAgentPath()

	// unload
	exec.Command("launchctl", "unload", plistPath).Run()

	// remove plist
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: cannot remove %s: %v\n", plistPath, err)
		os.Exit(1)
	}

	fmt.Println("✓ clipx LaunchAgent removed")
	fmt.Println("clipx will no longer start at login.")
}

func showStatus() {
	// check if the launch agent is loaded
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

	// restart LaunchAgent if installed
	plistPath := launchAgentPath()
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Println("restarting LaunchAgent...")
		exec.Command("launchctl", "unload", plistPath).Run()
		exec.Command("launchctl", "load", plistPath).Run()
		fmt.Println("✓ LaunchAgent restarted")
	}
}

// --- Clipboard (macOS native via pbcopy/pbpaste) ---

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

// --- UDP Multicast Beacon ---

// findMulticastInterface finds the best network interface for multicast (WiFi or Ethernet).
func findMulticastInterface() *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		// skip loopback, down, and non-multicast interfaces
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		// must have a non-loopback IPv4 address
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() {
				return &iface
			}
		}
	}
	return nil
}

func (n *Node) startBeacon() {
	dst, err := net.ResolveUDPAddr("udp4", multicastAddr)
	if err != nil {
		n.logger.Fatalf("resolve multicast: %v", err)
	}

	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		n.logger.Fatalf("listen packet: %v", err)
	}
	defer conn.Close()

	p := ipv4.NewPacketConn(conn)

	bindInterface := func() {
		if iface := findMulticastInterface(); iface != nil {
			if err := p.SetMulticastInterface(iface); err != nil {
				n.logger.Printf("warning: set multicast interface: %v", err)
			} else {
				n.logger.Printf("multicast send bound to %s", iface.Name)
			}
		}
	}

	bindInterface()
	p.SetMulticastTTL(2)
	p.SetMulticastLoopback(true)

	msg := []byte(fmt.Sprintf("%s|%s|%d", magicHeader, n.id, tcpPort))
	rebindTicker := time.NewTicker(30 * time.Second)
	defer rebindTicker.Stop()

	for {
		select {
		case <-rebindTicker.C:
			// periodically re-bind in case network changed
			bindInterface()
		default:
		}

		_, err := conn.WriteTo(msg, dst)
		if err != nil {
			n.logger.Printf("beacon send error: %v", err)
			time.Sleep(5 * time.Second)
			bindInterface()
			continue
		}
		time.Sleep(beaconInterval)
	}
}

// multicastInterfaces returns all IPv4-capable, up, multicast, non-loopback interfaces.
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

func (n *Node) listenBeacon() {
	group := net.ParseIP("239.77.77.77")

	// use ListenConfig with SO_REUSEADDR + SO_REUSEPORT for reliable multicast on macOS
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

	conn, err := lc.ListenPacket(nil, "udp4", ":9877")
	if err != nil {
		n.logger.Fatalf("listen multicast: %v", err)
	}
	defer conn.Close()

	p := ipv4.NewPacketConn(conn)

	// join multicast group and periodically re-join to handle network changes
	joinAll := func() int {
		joined := 0
		for _, iface := range multicastInterfaces() {
			// leave first (ignore error — may not be joined)
			p.LeaveGroup(&iface, &net.UDPAddr{IP: group})
			if err := p.JoinGroup(&iface, &net.UDPAddr{IP: group}); err != nil {
				continue
			}
			n.logger.Printf("joined multicast on %s", iface.Name)
			joined++
		}
		return joined
	}

	if joinAll() == 0 {
		n.logger.Fatal("could not join multicast group on any interface")
	}

	// periodically re-join to stay fresh (every 30s)
	go func() {
		for {
			time.Sleep(30 * time.Second)
			joinAll()
		}
	}()

	p.SetControlMessage(ipv4.FlagDst, true)

	buf := make([]byte, 1024)

	for {
		nBytes, cm, src, err := p.ReadFrom(buf)
		if err != nil {
			n.logger.Printf("beacon recv error: %v", err)
			continue
		}

		// only process packets sent to our multicast group
		if cm != nil && cm.Dst != nil && !cm.Dst.Equal(group) {
			continue
		}

		parts := strings.Split(string(buf[:nBytes]), "|")
		if len(parts) != 3 || parts[0] != magicHeader {
			continue
		}

		peerID := parts[1]
		if peerID == n.id {
			continue // ignore self
		}

		peerPort := parts[2]
		var srcIP string
		if udpAddr, ok := src.(*net.UDPAddr); ok {
			srcIP = udpAddr.IP.String()
		} else {
			host, _, _ := net.SplitHostPort(src.String())
			srcIP = host
		}
		peerAddr := fmt.Sprintf("%s:%s", srcIP, peerPort)

		n.peersMu.Lock()
		_, found := n.peers[peerID]
		if !found {
			n.logger.Printf("discovered peer %s at %s", peerID, peerAddr)
		}
		n.peers[peerID] = peerInfo{addr: peerAddr, lastSeen: time.Now()}
		n.peersMu.Unlock()
	}
}

func (n *Node) cleanupPeers() {
	for {
		time.Sleep(10 * time.Second)
		n.peersMu.Lock()
		for id, p := range n.peers {
			if time.Since(p.lastSeen) > 10*time.Second {
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
	}
}

// --- TCP Sync ---

func (n *Node) listenTCP() {
	ln, err := net.Listen("tcp4", fmt.Sprintf(":%d", tcpPort))
	if err != nil {
		n.logger.Fatalf("tcp listen: %v", err)
	}
	defer ln.Close()
	n.logger.Printf("listening on tcp :%d", tcpPort)

	for {
		conn, err := ln.Accept()
		if err != nil {
			n.logger.Printf("tcp accept: %v", err)
			continue
		}
		go n.handleIncoming(conn)
	}
}

func (n *Node) handleIncoming(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	// protocol: 6 bytes magic + 8 bytes nodeID + 4 bytes length + data
	header := make([]byte, 6)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	if string(header) != magicHeader {
		return
	}

	nodeIDBuf := make([]byte, 8)
	if _, err := io.ReadFull(conn, nodeIDBuf); err != nil {
		return
	}
	senderID := string(nodeIDBuf)

	if senderID == n.id {
		return
	}

	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return
	}
	dataLen := binary.BigEndian.Uint32(lenBuf)

	if dataLen > maxClipSize {
		n.logger.Printf("rejected oversized clip from %s (%d bytes)", senderID, dataLen)
		return
	}

	data := make([]byte, dataLen)
	if _, err := io.ReadFull(conn, data); err != nil {
		return
	}

	hash := hashContent(data)

	// check if this is the same as our current clipboard
	n.mu.Lock()
	if hash == n.lastHash {
		n.mu.Unlock()
		return
	}
	n.lastHash = hash
	n.mu.Unlock()

	// remember this hash came from a peer
	n.peerHashesMu.Lock()
	n.peerHashes[hash] = time.Now()
	n.peerHashesMu.Unlock()

	if err := writeClipboard(data); err != nil {
		n.logger.Printf("clipboard write error: %v", err)
		return
	}

	preview := string(data)
	if len(preview) > 60 {
		preview = preview[:60] + "..."
	}
	preview = strings.ReplaceAll(preview, "\n", "⏎")
	n.logger.Printf("← recv from %s (%d bytes): %s", senderID, dataLen, preview)
}

func (n *Node) sendToPeer(addr string, data []byte) error {
	conn, err := net.DialTimeout("tcp4", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// write magic
	if _, err := conn.Write([]byte(magicHeader)); err != nil {
		return err
	}

	// write our node ID (padded/truncated to 8 bytes)
	nodeID := fmt.Sprintf("%-8s", n.id)
	if _, err := conn.Write([]byte(nodeID[:8])); err != nil {
		return err
	}

	// write length + data
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := conn.Write(lenBuf); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}

	return nil
}

// --- Clipboard Watcher ---

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

		preview := string(data)
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", "⏎")
		n.logger.Printf("→ broadcasting (%d bytes): %s", len(data), preview)

		n.peersMu.Lock()
		peers := make(map[string]peerInfo)
		for k, v := range n.peers {
			peers[k] = v
		}
		n.peersMu.Unlock()

		for peerID, peer := range peers {
			if err := n.sendToPeer(peer.addr, data); err != nil {
				n.logger.Printf("send to %s failed: %v", peerID, err)
				// remove unreachable peer — it'll be re-discovered via beacon
				n.peersMu.Lock()
				delete(n.peers, peerID)
				n.peersMu.Unlock()
			}
		}
	}
}
