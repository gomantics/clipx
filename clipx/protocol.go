package clipx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// Message types
const (
	msgClip = byte('C') // clipboard data
	msgPing = byte('P') // ping
	msgPong = byte('A') // pong (ack)
)

// Wire format:
//   [6 bytes magic "CLIPX2"] [1 byte type] [8 bytes nodeID] [payload...]
//
// Clipboard payload: [64 bytes sha256 hex] [data...]
// Ping/Pong payload: (empty)

const magic = "CLIPX2"
const headerLen = 6 + 1 + 8 // magic + type + nodeID
const hashLen = 64

// encodeMessage builds a wire message.
func encodeMessage(msgType byte, nodeID string, payload []byte) []byte {
	id := fmt.Sprintf("%-8s", nodeID)[:8]
	msg := make([]byte, 0, headerLen+len(payload))
	msg = append(msg, []byte(magic)...)
	msg = append(msg, msgType)
	msg = append(msg, []byte(id)...)
	msg = append(msg, payload...)
	return msg
}

// decodeMessage parses a wire message. Returns type, nodeID, payload, error.
func decodeMessage(data []byte) (byte, string, []byte, error) {
	if len(data) < headerLen {
		return 0, "", nil, errors.New("message too short")
	}
	if string(data[:6]) != magic {
		return 0, "", nil, errors.New("bad magic")
	}
	msgType := data[6]
	nodeID := string(data[7:15])
	payload := data[headerLen:]
	return msgType, nodeID, payload, nil
}

// encodeClipPayload builds the payload for a clipboard message.
func encodeClipPayload(data []byte) []byte {
	hash := HashContent(data)
	payload := make([]byte, 0, hashLen+len(data))
	payload = append(payload, []byte(hash)...)
	payload = append(payload, data...)
	return payload
}

// decodeClipPayload parses the payload of a clipboard message.
func decodeClipPayload(payload []byte) (hash string, data []byte, err error) {
	if len(payload) < hashLen {
		return "", nil, errors.New("clip payload too short")
	}
	return string(payload[:hashLen]), payload[hashLen:], nil
}

// HashContent returns the SHA-256 hex hash of data.
func HashContent(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
