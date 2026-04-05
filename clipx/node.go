package clipx

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	maxClipSize  = 64 * 1024 // 64KB safe for UDP on LAN
	pollInterval = 500 * time.Millisecond
)

// Node is a clipx daemon instance.
type Node struct {
	id        string
	peers     []string // peer IPs
	clipboard Clipboard
	logger    *log.Logger
	conn      net.PacketConn

	mu       sync.Mutex
	lastHash string

	// hashes received from peers — prevents re-broadcasting
	peerHashes   map[string]time.Time
	peerHashesMu sync.Mutex

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewNode creates a new clipx node.
func NewNode(cfg *Config, logger *log.Logger) (*Node, error) {
	return NewNodeWithClipboard(cfg, logger, &MacClipboard{})
}

// NewNodeWithClipboard creates a node with a custom clipboard (for testing).
func NewNodeWithClipboard(cfg *Config, logger *log.Logger, cb Clipboard) (*Node, error) {
	conn, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", DefaultPort))
	if err != nil {
		return nil, fmt.Errorf("listen udp :%d: %w", DefaultPort, err)
	}

	n := &Node{
		id:         uuid.New().String()[:8],
		peers:      cfg.Peers,
		clipboard:  cb,
		logger:     logger,
		conn:       conn,
		peerHashes: make(map[string]time.Time),
		stopCh:     make(chan struct{}),
	}

	// initialize hash with current clipboard
	if content, err := cb.Read(); err == nil && len(content) > 0 {
		n.lastHash = HashContent(content)
	}

	return n, nil
}

// Start begins the listener, clipboard watcher, and heartbeat.
func (n *Node) Start() {
	n.wg.Add(3)
	go n.listen()
	go n.watchClipboard()
	go n.heartbeat()
}

// Stop shuts down the node.
func (n *Node) Stop() {
	close(n.stopCh)
	n.conn.Close()
	n.wg.Wait()
}

// --- Listener ---

func (n *Node) listen() {
	defer n.wg.Done()
	buf := make([]byte, 70*1024) // 64KB + header room

	for {
		n.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		nBytes, remoteAddr, err := n.conn.ReadFrom(buf)
		if err != nil {
			select {
			case <-n.stopCh:
				return
			default:
				if !isTimeout(err) {
					n.logger.Printf("recv error: %v", err)
				}
				continue
			}
		}

		msgType, senderID, payload, err := decodeMessage(buf[:nBytes])
		if err != nil {
			continue
		}

		senderID = strings.TrimSpace(senderID)

		// ignore our own messages (shouldn't happen with unicast, but safe)
		if senderID == n.id {
			continue
		}

		switch msgType {
		case msgPing:
			// respond with pong
			pong := encodeMessage(msgPong, n.id, nil)
			n.conn.WriteTo(pong, remoteAddr)

		case msgPong:
			// handled by PingPeer directly

		case msgClip:
			n.handleClip(senderID, payload)
		}
	}
}

func (n *Node) handleClip(senderID string, payload []byte) {
	hash, data, err := decodeClipPayload(payload)
	if err != nil {
		return
	}

	n.mu.Lock()
	if hash == n.lastHash {
		n.mu.Unlock()
		return
	}
	n.lastHash = hash
	n.mu.Unlock()

	// remember peer hash to avoid re-broadcasting
	n.peerHashesMu.Lock()
	n.peerHashes[hash] = time.Now()
	n.peerHashesMu.Unlock()

	if err := n.clipboard.Write(data); err != nil {
		n.logger.Printf("clipboard write error: %v", err)
		return
	}

	preview := clipPreview(data)
	n.logger.Printf("← from %s (%d bytes): %s", senderID, len(data), preview)
}

// --- Clipboard watcher ---

func (n *Node) watchClipboard() {
	defer n.wg.Done()

	for {
		select {
		case <-n.stopCh:
			return
		case <-time.After(pollInterval):
		}

		data, err := n.clipboard.Read()
		if err != nil || len(data) == 0 || len(data) > maxClipSize {
			continue
		}

		hash := HashContent(data)

		n.mu.Lock()
		if hash == n.lastHash {
			n.mu.Unlock()
			continue
		}
		n.lastHash = hash
		n.mu.Unlock()

		// skip if this came from a peer
		n.peerHashesMu.Lock()
		if _, fromPeer := n.peerHashes[hash]; fromPeer {
			n.peerHashesMu.Unlock()
			continue
		}
		n.peerHashesMu.Unlock()

		if len(n.peers) == 0 {
			continue
		}

		preview := clipPreview(data)
		n.logger.Printf("→ sending to %d peer(s) (%d bytes): %s", len(n.peers), len(data), preview)

		msg := encodeMessage(msgClip, n.id, encodeClipPayload(data))
		for _, peer := range n.peers {
			if err := sendUDP(peer, msg); err != nil {
				n.logger.Printf("send to %s failed: %v", peer, err)
			}
		}
	}
}

// --- Heartbeat (peer hash cleanup) ---

func (n *Node) heartbeat() {
	defer n.wg.Done()

	for {
		select {
		case <-n.stopCh:
			return
		case <-time.After(15 * time.Second):
		}

		n.peerHashesMu.Lock()
		for h, t := range n.peerHashes {
			if time.Since(t) > 60*time.Second {
				delete(n.peerHashes, h)
			}
		}
		n.peerHashesMu.Unlock()
	}
}

// --- Helpers ---

// sendUDP sends data to a peer via a fresh UDP connection.
// Using Dial lets the OS pick the right interface/route.
func sendUDP(peer string, data []byte) error {
	addr := net.JoinHostPort(peer, fmt.Sprintf("%d", DefaultPort))
	conn, err := net.DialTimeout("udp4", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write(data)
	return err
}

func clipPreview(data []byte) string {
	s := string(data)
	if len(s) > 60 {
		s = s[:60] + "..."
	}
	s = strings.ReplaceAll(s, "\n", "⏎")
	return s
}

func isTimeout(err error) bool {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return false
}
