package clipx

import (
	"fmt"
	"net"
	"time"
)

// DefaultPort is the UDP port used for all clipx communication
// (clipboard sync, ping/pong health checks).
const DefaultPort = 9877

// ResolveAddr resolves a hostname or IP string to an IPv4 address.
// If addr is already a valid IP, it is returned as-is.
func ResolveAddr(addr string) (string, error) {
	// if it's already an IP, return as-is
	if ip := net.ParseIP(addr); ip != nil {
		return ip.String(), nil
	}
	// try DNS resolution
	ips, err := net.LookupIP(addr)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s: %w", addr, err)
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address found for %s", addr)
}

// PingPeer sends a UDP ping to the given address and waits up to 1 second
// for a pong response. Returns "● online" or "○ offline".
func PingPeer(addr string) string {
	target := net.JoinHostPort(addr, fmt.Sprintf("%d", DefaultPort))
	conn, err := net.DialTimeout("udp4", target, 1*time.Second)
	if err != nil {
		return "○ offline"
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(1 * time.Second))

	msg := encodeMessage(msgPing, "ping0000", nil)
	if _, err := conn.Write(msg); err != nil {
		return "○ offline"
	}

	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return "○ offline"
	}

	msgType, _, _, err := decodeMessage(buf[:n])
	if err != nil || msgType != msgPong {
		return "○ offline"
	}

	return "● online"
}
