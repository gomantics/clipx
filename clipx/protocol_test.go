package clipx

import (
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
			// nodeID is padded/truncated to 8 chars
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
