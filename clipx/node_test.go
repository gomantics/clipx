package clipx

import (
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockClipboard is a thread-safe in-memory clipboard for testing.
type mockClipboard struct {
	mu   sync.Mutex
	data []byte
}

func (m *mockClipboard) Read() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]byte, len(m.data))
	copy(out, m.data)
	return out, nil
}

func (m *mockClipboard) Write(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make([]byte, len(data))
	copy(m.data, data)
	return nil
}

func (m *mockClipboard) Get() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.data)
}

func (m *mockClipboard) Set(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = []byte(s)
}

func newTestLogger(t *testing.T) *log.Logger {
	return log.New(os.Stderr, "[test] ", log.LstdFlags|log.Lmsgprefix)
}

func TestTwoNodeSync(t *testing.T) {
	testDirectSendReceive(t)
}

func testDirectSendReceive(t *testing.T) {
	cb1 := &mockClipboard{data: []byte("initial")}
	cb2 := &mockClipboard{data: []byte("initial")}

	logger := newTestLogger(t)

	// create two nodes on random ports
	conn1, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn2, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	port1 := conn1.LocalAddr().(*net.UDPAddr).Port
	port2 := conn2.LocalAddr().(*net.UDPAddr).Port

	node1 := &Node{
		id:         "node1111",
		peers:      nil, // will send manually
		clipboard:  cb1,
		logger:     logger,
		conn:       conn1,
		peerHashes: make(map[string]time.Time),
		chunks:     make(map[string]*chunkBuffer),
		stopCh:     make(chan struct{}),
		lastHash:   HashContent([]byte("initial")),
	}

	node2 := &Node{
		id:         "node2222",
		peers:      nil,
		clipboard:  cb2,
		logger:     logger,
		conn:       conn2,
		peerHashes: make(map[string]time.Time),
		chunks:     make(map[string]*chunkBuffer),
		stopCh:     make(chan struct{}),
		lastHash:   HashContent([]byte("initial")),
	}

	// start listeners
	node1.wg.Add(1)
	go node1.listen()
	node2.wg.Add(1)
	go node2.listen()

	time.Sleep(100 * time.Millisecond)

	// node1 sends clipboard to node2
	clipData := []byte("hello from node1")
	msg := encodeMessage(msgClip, node1.id, encodeClipPayload(clipData))
	_, err = conn1.WriteTo(msg, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port2})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	// wait for node2 to process
	time.Sleep(500 * time.Millisecond)

	got := cb2.Get()
	if got != "hello from node1" {
		t.Errorf("node2 clipboard: got %q, want %q", got, "hello from node1")
	}

	// verify node2's lastHash updated
	node2.mu.Lock()
	if node2.lastHash != HashContent(clipData) {
		t.Error("node2 lastHash not updated")
	}
	node2.mu.Unlock()

	// verify node2 recorded peer hash (won't re-broadcast)
	node2.peerHashesMu.Lock()
	if _, ok := node2.peerHashes[HashContent(clipData)]; !ok {
		t.Error("node2 should have recorded peer hash")
	}
	node2.peerHashesMu.Unlock()

	// now node2 sends to node1
	clipData2 := []byte("hello from node2")
	msg2 := encodeMessage(msgClip, node2.id, encodeClipPayload(clipData2))
	_, err = conn2.WriteTo(msg2, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port1})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	got = cb1.Get()
	if got != "hello from node2" {
		t.Errorf("node1 clipboard: got %q, want %q", got, "hello from node2")
	}

	// cleanup
	close(node1.stopCh)
	close(node2.stopCh)
	conn1.Close()
	conn2.Close()
	node1.wg.Wait()
	node2.wg.Wait()
}

func TestDuplicateSuppression(t *testing.T) {
	cb := &mockClipboard{data: []byte("original")}
	logger := newTestLogger(t)

	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port

	node := &Node{
		id:         "testnode",
		clipboard:  cb,
		logger:     logger,
		conn:       conn,
		peerHashes: make(map[string]time.Time),
		chunks:     make(map[string]*chunkBuffer),
		stopCh:     make(chan struct{}),
		lastHash:   HashContent([]byte("original")),
	}

	node.wg.Add(1)
	go node.listen()

	time.Sleep(100 * time.Millisecond)

	// send same content twice
	clipData := []byte("duplicate test")
	msg := encodeMessage(msgClip, "sender01", encodeClipPayload(clipData))

	sender, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	defer sender.Close()

	// first send — should update clipboard
	sender.WriteTo(msg, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	time.Sleep(300 * time.Millisecond)

	if cb.Get() != "duplicate test" {
		t.Fatalf("first send should update clipboard, got %q", cb.Get())
	}

	// manually change clipboard to something else
	cb.Set("changed")
	node.mu.Lock()
	node.lastHash = HashContent([]byte("changed"))
	node.mu.Unlock()

	// second send with same data — hash is in peerHashes but lastHash changed,
	// so it should still update (different lastHash)
	sender.WriteTo(msg, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	time.Sleep(300 * time.Millisecond)

	if cb.Get() != "duplicate test" {
		t.Errorf("second send should update (lastHash was different), got %q", cb.Get())
	}

	// third send — now lastHash matches, should be skipped
	cb.Set("should not change")
	sender.WriteTo(msg, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	time.Sleep(300 * time.Millisecond)

	if cb.Get() != "should not change" {
		t.Errorf("third send should be skipped (same hash), got %q", cb.Get())
	}

	close(node.stopCh)
	conn.Close()
	node.wg.Wait()
}

func TestPingPong(t *testing.T) {
	cb := &mockClipboard{}
	logger := newTestLogger(t)

	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port

	node := &Node{
		id:         "pingnode",
		clipboard:  cb,
		logger:     logger,
		conn:       conn,
		peerHashes: make(map[string]time.Time),
		chunks:     make(map[string]*chunkBuffer),
		stopCh:     make(chan struct{}),
	}

	node.wg.Add(1)
	go node.listen()

	time.Sleep(100 * time.Millisecond)

	// send a ping
	sender, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	defer sender.Close()

	ping := encodeMessage(msgPing, "pinger01", nil)
	sender.WriteTo(ping, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})

	// read pong
	sender.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, _, err := sender.ReadFrom(buf)
	if err != nil {
		t.Fatalf("expected pong, got error: %v", err)
	}

	msgType, nodeID, _, err := decodeMessage(buf[:n])
	if err != nil {
		t.Fatalf("decode pong: %v", err)
	}
	if msgType != msgPong {
		t.Errorf("expected pong type, got %c", msgType)
	}
	if strings.TrimSpace(nodeID) != "pingnode" {
		t.Errorf("pong nodeID: got %q, want %q", nodeID, "pingnode")
	}

	close(node.stopCh)
	conn.Close()
	node.wg.Wait()
}

func TestSelfMessageIgnored(t *testing.T) {
	cb := &mockClipboard{data: []byte("original")}
	logger := newTestLogger(t)

	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port

	node := &Node{
		id:         "selfnode",
		clipboard:  cb,
		logger:     logger,
		conn:       conn,
		peerHashes: make(map[string]time.Time),
		chunks:     make(map[string]*chunkBuffer),
		stopCh:     make(chan struct{}),
		lastHash:   HashContent([]byte("original")),
	}

	node.wg.Add(1)
	go node.listen()

	time.Sleep(100 * time.Millisecond)

	// send a message FROM our own node ID
	msg := encodeMessage(msgClip, "selfnode", encodeClipPayload([]byte("should be ignored")))
	sender, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	defer sender.Close()
	sender.WriteTo(msg, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})

	time.Sleep(300 * time.Millisecond)

	if cb.Get() != "original" {
		t.Errorf("self message should be ignored, clipboard changed to %q", cb.Get())
	}

	close(node.stopCh)
	conn.Close()
	node.wg.Wait()
}

func TestChunkedTransfer(t *testing.T) {
	// use 3x 1KB chunks to avoid OS UDP buffer issues in tests
	chunkSize := 1024
	bigContent := []byte(strings.Repeat("abcdefghij", (chunkSize*3)/10))
	if len(bigContent) <= chunkSize {
		t.Fatal("test content should be larger than chunkSize")
	}

	cb := &mockClipboard{data: []byte("original")}
	logger := newTestLogger(t)

	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port

	node := &Node{
		id:         "receiver",
		clipboard:  cb,
		logger:     logger,
		conn:       conn,
		peerHashes: make(map[string]time.Time),
		chunks:     make(map[string]*chunkBuffer),
		stopCh:     make(chan struct{}),
		lastHash:   HashContent([]byte("original")),
	}

	node.wg.Add(1)
	go node.listen()
	time.Sleep(100 * time.Millisecond)

	hash := HashContent(bigContent)
	totalChunks := (len(bigContent) + chunkSize - 1) / chunkSize

	sender, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	defer sender.Close()

	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(bigContent) {
			end = len(bigContent)
		}
		payload := encodeChunkPayload(hash, uint16(i), uint16(totalChunks), bigContent[start:end])
		msg := encodeMessage(msgChunk, "sender01", payload)
		sender.WriteTo(msg, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
		time.Sleep(1 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	got := cb.Get()
	if got != string(bigContent) {
		t.Errorf("chunked transfer: got %d bytes, want %d bytes", len(got), len(bigContent))
	}

	node.chunksMu.Lock()
	if len(node.chunks) != 0 {
		t.Errorf("chunk buffer should be empty, got %d", len(node.chunks))
	}
	node.chunksMu.Unlock()

	close(node.stopCh)
	conn.Close()
	node.wg.Wait()
}

func TestUTF8ClipboardSync(t *testing.T) {
	utf8Content := []byte("hello \u2014 world \U0001f30d \u00fcber caf\u00e9 na\u00efve r\u00e9sum\u00e9")

	cb := &mockClipboard{data: []byte("original")}
	logger := newTestLogger(t)

	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port

	node := &Node{
		id:         "utf8node",
		clipboard:  cb,
		logger:     logger,
		conn:       conn,
		peerHashes: make(map[string]time.Time),
		chunks:     make(map[string]*chunkBuffer),
		stopCh:     make(chan struct{}),
		lastHash:   HashContent([]byte("original")),
	}

	node.wg.Add(1)
	go node.listen()
	time.Sleep(100 * time.Millisecond)

	msg := encodeMessage(msgClip, "sender01", encodeClipPayload(utf8Content))
	sender, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	defer sender.Close()
	sender.WriteTo(msg, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})

	time.Sleep(300 * time.Millisecond)

	got := cb.Get()
	if got != string(utf8Content) {
		t.Errorf("UTF-8 corrupted:\ngot:  %q\nwant: %q", got, string(utf8Content))
	}

	close(node.stopCh)
	conn.Close()
	node.wg.Wait()
}
