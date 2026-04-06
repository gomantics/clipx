// Package clipx implements LAN clipboard sync for macOS.
//
// It uses UDP unicast to send clipboard content between explicitly
// paired peers on the same local network. Content is deduplicated
// via SHA-256 hashing to prevent infinite ping-pong loops.
package clipx

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
)

// Clipboard abstracts clipboard read/write operations.
// Implementations must be safe for concurrent use.
type Clipboard interface {
	// Read returns the current clipboard content.
	Read() ([]byte, error)
	// Write sets the clipboard content.
	Write(data []byte) error
}

// MacClipboard reads and writes the macOS system clipboard
// using pbcopy and pbpaste.
type MacClipboard struct{}

// utf8Env returns the current environment with LANG forced to UTF-8.
// LaunchAgents run with a minimal env where LANG is unset,
// causing pbcopy/pbpaste to default to ASCII and corrupt multi-byte chars.
var utf8Env = func() []string {
	env := os.Environ()
	hasLang := false
	for i, e := range env {
		if len(e) > 5 && e[:5] == "LANG=" {
			if !strings.Contains(e, "UTF-8") {
				env[i] = "LANG=en_US.UTF-8"
			}
			hasLang = true
			break
		}
	}
	if !hasLang {
		env = append(env, "LANG=en_US.UTF-8")
	}
	return env
}()

func (c *MacClipboard) Read() ([]byte, error) {
	cmd := exec.Command("pbpaste")
	cmd.Env = utf8Env
	return cmd.Output()
}

func (c *MacClipboard) Write(data []byte) error {
	cmd := exec.Command("pbcopy")
	cmd.Env = utf8Env
	cmd.Stdin = bytes.NewReader(data)
	return cmd.Run()
}
