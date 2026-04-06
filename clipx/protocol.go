package clipx

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// Message types
const (
	msgClip  = byte('C') // clipboard data (single packet, <=32KB payload)
	msgChunk = byte('K') // clipboard chunk (for large content)
	msgPing  = byte('P') // ping
	msgPong  = byte('A') // pong (ack)
)

// Wire format:
//   [6 bytes magic "CLIPX2"] [1 byte type] [8 bytes nodeID] [payload...]
//
// Clipboard payload (msgClip):
//   [64 bytes sha256 hex] [data...]
//
// Chunk payload (msgChunk):
//   [64 bytes sha256 hex] [2 bytes chunkIndex BE] [2 bytes totalChunks BE] [data...]
//
// Ping/Pong payload: (empty)

const magic = "CLIPX2"
const headerLen = 6 + 1 + 8 // magic + type + nodeID
const hashLen = 64
const chunkHeaderLen = hashLen + 2 + 2 // hash + index + total

const (
	// MaxChunkPayload is the max clipboard data per UDP packet.
	// Must stay under WiFi MTU (~1500) minus headers to avoid
	// "message too long" on macOS which sets DF (Don't Fragment).
	// 1500 MTU - 20 IP - 8 UDP - 15 clipx header - 68 chunk header = 1389
	MaxChunkPayload = 1300 // safe margin under any MTU

	// MaxClipSize is the absolute max clipboard size we'll sync.
	MaxClipSize = 10 * 1024 * 1024 // 10MB
)

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

// encodeClipPayload builds the payload for a single-packet clipboard message.
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

// encodeChunkPayload builds the payload for a chunk message.
func encodeChunkPayload(hash string, index, total uint16, data []byte) []byte {
	payload := make([]byte, 0, chunkHeaderLen+len(data))
	payload = append(payload, []byte(hash)...)
	payload = binary.BigEndian.AppendUint16(payload, index)
	payload = binary.BigEndian.AppendUint16(payload, total)
	payload = append(payload, data...)
	return payload
}

// decodeChunkPayload parses the payload of a chunk message.
func decodeChunkPayload(payload []byte) (hash string, index, total uint16, data []byte, err error) {
	if len(payload) < chunkHeaderLen {
		return "", 0, 0, nil, errors.New("chunk payload too short")
	}
	hash = string(payload[:hashLen])
	index = binary.BigEndian.Uint16(payload[hashLen : hashLen+2])
	total = binary.BigEndian.Uint16(payload[hashLen+2 : hashLen+4])
	data = payload[chunkHeaderLen:]
	return hash, index, total, data, nil
}

// HashContent returns the SHA-256 hex hash of data.
func HashContent(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
