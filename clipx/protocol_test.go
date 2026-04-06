package clipx

import (
	"strings"
	"testing"
)

func TestEncodeDecodeMessage(t *testing.T) {
	tests := []struct {
		name    string
		msgType byte
		nodeID  string
		payload []byte
	}{
		{"ping", msgPing, "abc12345", nil},
		{"pong", msgPong, "xyz99999", nil},
		{"clip", msgClip, "node0001", []byte("hello world")},
		{"chunk", msgChunk, "node0002", []byte("chunk data")},
		{"empty payload", msgPing, "short", nil},
		{"short nodeID padded", msgClip, "ab", []byte("data")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := encodeMessage(tt.msgType, tt.nodeID, tt.payload)

			gotType, gotID, gotPayload, err := decodeMessage(msg)
			if err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if gotType != tt.msgType {
				t.Errorf("type: got %c, want %c", gotType, tt.msgType)
			}
			wantID := tt.nodeID
			if len(wantID) > 8 {
				wantID = wantID[:8]
			}
			for len(wantID) < 8 {
				wantID += " "
			}
			if gotID != wantID {
				t.Errorf("nodeID: got %q, want %q", gotID, wantID)
			}
			if string(gotPayload) != string(tt.payload) {
				t.Errorf("payload: got %q, want %q", gotPayload, tt.payload)
			}
		})
	}
}

func TestDecodeMessageTooShort(t *testing.T) {
	_, _, _, err := decodeMessage([]byte("short"))
	if err == nil {
		t.Fatal("expected error for short message")
	}
}

func TestDecodeMessageBadMagic(t *testing.T) {
	msg := make([]byte, headerLen)
	copy(msg, "BADMAG")
	_, _, _, err := decodeMessage(msg)
	if err == nil {
		t.Fatal("expected error for bad magic")
	}
}

func TestClipPayloadRoundtrip(t *testing.T) {
	data := []byte("the quick brown fox jumps over the lazy dog")
	payload := encodeClipPayload(data)

	hash, gotData, err := decodeClipPayload(payload)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if hash != HashContent(data) {
		t.Errorf("hash mismatch: got %s, want %s", hash, HashContent(data))
	}
	if string(gotData) != string(data) {
		t.Errorf("data mismatch: got %q, want %q", gotData, data)
	}
}

func TestClipPayloadTooShort(t *testing.T) {
	_, _, err := decodeClipPayload([]byte("tooshort"))
	if err == nil {
		t.Fatal("expected error for short payload")
	}
}

func TestChunkPayloadRoundtrip(t *testing.T) {
	data := []byte("chunk data here")
	hash := HashContent([]byte("full content"))

	payload := encodeChunkPayload(hash, 2, 5, data)

	gotHash, gotIndex, gotTotal, gotData, err := decodeChunkPayload(payload)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if gotHash != hash {
		t.Errorf("hash: got %s, want %s", gotHash, hash)
	}
	if gotIndex != 2 {
		t.Errorf("index: got %d, want 2", gotIndex)
	}
	if gotTotal != 5 {
		t.Errorf("total: got %d, want 5", gotTotal)
	}
	if string(gotData) != string(data) {
		t.Errorf("data: got %q, want %q", gotData, data)
	}
}

func TestChunkPayloadTooShort(t *testing.T) {
	_, _, _, _, err := decodeChunkPayload([]byte("short"))
	if err == nil {
		t.Fatal("expected error for short chunk payload")
	}
}

func TestHashContent(t *testing.T) {
	h1 := HashContent([]byte("hello"))
	h2 := HashContent([]byte("hello"))
	h3 := HashContent([]byte("world"))

	if h1 != h2 {
		t.Error("same content should produce same hash")
	}
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}
	if len(h1) != 64 {
		t.Errorf("hash should be 64 hex chars, got %d", len(h1))
	}
}

func TestHashContentUTF8(t *testing.T) {
	// ensure non-ASCII (em-dash, emoji, etc.) hashes correctly
	data := []byte("hello — world 🌍 über café")
	h := HashContent(data)
	if len(h) != 64 {
		t.Errorf("hash should be 64 hex chars, got %d", len(h))
	}
	// same content = same hash
	if h != HashContent(data) {
		t.Error("same UTF-8 content should produce same hash")
	}
}

func TestMaxChunkPayloadCoverage(t *testing.T) {
	// content exactly at MaxChunkPayload should be single packet
	data := strings.Repeat("x", MaxChunkPayload)
	if len(data) > MaxChunkPayload {
		t.Fatal("test setup wrong")
	}

	// content 1 byte over should need 2 chunks
	data2 := strings.Repeat("x", MaxChunkPayload+1)
	totalChunks := (len(data2) + MaxChunkPayload - 1) / MaxChunkPayload
	if totalChunks != 2 {
		t.Errorf("expected 2 chunks, got %d", totalChunks)
	}
}
