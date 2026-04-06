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
	pollInterval = 500 * time.Millisecond
)

// Node is a clipx daemon instance.
type Node struct {
	id        string
	peers     []string // peer IPs
	clipboard Clipboard
	logger    *log.Logger
	conn      net.PacketConn     // listener
	peerConns map[string]net.Conn // persistent send connections per peer

	mu           sync.Mutex
	lastHash     string // last clipboard hash we've seen (sent or received)

	// hashes received from peers — prevents re-broadcasting
	peerHashes   map[string]time.Time
	peerHashesMu sync.Mutex

	// chunk reassembly buffers
	chunks   map[string]*chunkBuffer
	chunksMu sync.Mutex

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// chunkBuffer collects chunks for a single transfer.
type chunkBuffer struct {
	hash      string
	total     int
	received  map[int][]byte
	createdAt time.Time
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
		peerConns:  make(map[string]net.Conn),
		peerHashes: make(map[string]time.Time),
		chunks:     make(map[string]*chunkBuffer),
		stopCh:     make(chan struct{}),
	}

	if content, err := cb.Read(); err == nil && len(content) > 0 {
		n.lastHash = HashContent(content)
	}

	// establish persistent connections to peers
	for _, peer := range cfg.Peers {
		n.connectPeer(peer)
	}

	return n, nil
}

// Start begins the listener, clipboard watcher, and maintenance.
func (n *Node) Start() {
	n.wg.Add(3)
	go n.listen()
	go n.watchClipboard()
	go n.maintenance()
}

// Stop shuts down the node.
func (n *Node) Stop() {
	close(n.stopCh)
	n.conn.Close()
	for _, c := range n.peerConns {
		c.Close()
	}
	n.wg.Wait()
}

func (n *Node) connectPeer(peer string) {
	addr := net.JoinHostPort(peer, fmt.Sprintf("%d", DefaultPort))
	conn, err := net.Dial("udp4", addr)
	if err != nil {
		n.logger.Printf("connect %s: %v", peer, err)
		return
	}
	n.peerConns[peer] = conn
	n.logger.Printf("connected to %s", peer)
}

func (n *Node) sendToPeer(peer string, data []byte) error {
	conn, ok := n.peerConns[peer]
	if !ok {
		n.connectPeer(peer)
		conn = n.peerConns[peer]
		if conn == nil {
			return fmt.Errorf("cannot connect to %s", peer)
		}
	}
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := conn.Write(data)
	if err != nil {
		// reconnect and retry once
		conn.Close()
		delete(n.peerConns, peer)
		n.connectPeer(peer)
		if c, ok := n.peerConns[peer]; ok {
			c.SetWriteDeadline(time.Now().Add(2 * time.Second))
			_, err = c.Write(data)
		}
	}
	return err
}

// --- Listener ---

func (n *Node) listen() {
	defer n.wg.Done()
	// set receive buffer large enough for max chunk + headers
	if udpConn, ok := n.conn.(*net.UDPConn); ok {
		udpConn.SetReadBuffer(256 * 1024)
	}
	buf := make([]byte, 40*1024) // 32KB payload + headers

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
		if senderID == n.id {
			continue
		}

		switch msgType {
		case msgPing:
			pong := encodeMessage(msgPong, n.id, nil)
			n.conn.WriteTo(pong, remoteAddr)

		case msgPong:
			// handled by PingPeer

		case msgClip:
			n.handleClip(senderID, payload)

		case msgChunk:
			n.handleChunk(senderID, payload)
		}
	}
}

func (n *Node) handleClip(senderID string, payload []byte) {
	hash, data, err := decodeClipPayload(payload)
	if err != nil {
		return
	}
	n.applyClipboard(senderID, hash, data)
}

func (n *Node) handleChunk(senderID string, payload []byte) {
	hash, index, total, data, err := decodeChunkPayload(payload)
	if err != nil {
		return
	}

	n.chunksMu.Lock()
	buf, exists := n.chunks[hash]
	if !exists {
		buf = &chunkBuffer{
			hash:      hash,
			total:     int(total),
			received:  make(map[int][]byte),
			createdAt: time.Now(),
		}
		n.chunks[hash] = buf
	}

	// store this chunk
	chunk := make([]byte, len(data))
	copy(chunk, data)
	buf.received[int(index)] = chunk

	// check if complete
	if len(buf.received) < buf.total {
		n.chunksMu.Unlock()
		return
	}

	// reassemble
	var assembled []byte
	for i := 0; i < buf.total; i++ {
		c, ok := buf.received[i]
		if !ok {
			n.chunksMu.Unlock()
			return // missing chunk
		}
		assembled = append(assembled, c...)
	}
	delete(n.chunks, hash)
	n.chunksMu.Unlock()

	// verify hash
	if HashContent(assembled) != hash {
		n.logger.Printf("chunk reassembly hash mismatch from %s", senderID)
		return
	}

	n.applyClipboard(senderID, hash, assembled)
}

func (n *Node) applyClipboard(senderID, hash string, data []byte) {
	n.mu.Lock()
	if hash == n.lastHash {
		n.mu.Unlock()
		return
	}
	n.lastHash = hash
	n.mu.Unlock()

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
		if err != nil || len(data) == 0 || len(data) > MaxClipSize {
			continue
		}

		hash := HashContent(data)

		// skip if this content was just received from a peer (anti-loop)
		n.peerHashesMu.Lock()
		if _, fromPeer := n.peerHashes[hash]; fromPeer {
			n.peerHashesMu.Unlock()
			continue
		}
		n.peerHashesMu.Unlock()

		// only send when clipboard content actually changes
		n.mu.Lock()
		if hash == n.lastHash {
			n.mu.Unlock()
			continue
		}
		n.lastHash = hash
		n.mu.Unlock()

		if len(n.peers) == 0 {
			continue
		}

		preview := clipPreview(data)
		n.logger.Printf("→ sending to %d peer(s) (%d bytes): %s", len(n.peers), len(data), preview)

		n.sendToAllPeers(data)
	}
}

func (n *Node) sendToAllPeers(data []byte) {
	hash := HashContent(data)

	if len(data) <= MaxChunkPayload {
		// single packet — send 3 times for UDP reliability
		msg := encodeMessage(msgClip, n.id, encodeClipPayload(data))
		for attempt := 0; attempt < 3; attempt++ {
			for _, peer := range n.peers {
				if err := n.sendToPeer(peer, msg); err != nil {
					n.logger.Printf("send to %s failed: %v", peer, err)
				}
			}
			if attempt < 2 {
				time.Sleep(50 * time.Millisecond)
			}
		}
		return
	}

	// chunked transfer
	totalChunks := (len(data) + MaxChunkPayload - 1) / MaxChunkPayload
	if totalChunks > 65535 {
		n.logger.Printf("content too large to chunk (%d bytes)", len(data))
		return
	}

	for i := 0; i < totalChunks; i++ {
		start := i * MaxChunkPayload
		end := start + MaxChunkPayload
		if end > len(data) {
			end = len(data)
		}
		chunk := data[start:end]
		payload := encodeChunkPayload(hash, uint16(i), uint16(totalChunks), chunk)
		msg := encodeMessage(msgChunk, n.id, payload)

		for _, peer := range n.peers {
			if err := n.sendToPeer(peer, msg); err != nil {
				n.logger.Printf("send chunk %d/%d to %s failed: %v", i+1, totalChunks, peer, err)
			}
		}

		// small delay between chunks to avoid overwhelming the receiver
		if i < totalChunks-1 {
			time.Sleep(1 * time.Millisecond)
		}
	}
}

// --- Maintenance ---

func (n *Node) maintenance() {
	defer n.wg.Done()

	for {
		select {
		case <-n.stopCh:
			return
		case <-time.After(15 * time.Second):
		}

		// cleanup old peer hashes
		n.peerHashesMu.Lock()
		for h, t := range n.peerHashes {
			if time.Since(t) > 60*time.Second {
				delete(n.peerHashes, h)
			}
		}
		n.peerHashesMu.Unlock()

		// cleanup stale chunk buffers (incomplete transfers)
		n.chunksMu.Lock()
		for hash, buf := range n.chunks {
			if time.Since(buf.createdAt) > 30*time.Second {
				n.logger.Printf("discarding incomplete transfer %s (%d/%d chunks)", hash[:8], len(buf.received), buf.total)
				delete(n.chunks, hash)
			}
		}
		n.chunksMu.Unlock()
	}
}

// --- Helpers ---

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
