package clipx

import (
	"bytes"
	"os/exec"
)

// Clipboard abstracts clipboard read/write for testability.
type Clipboard interface {
	Read() ([]byte, error)
	Write(data []byte) error
}

// MacClipboard uses pbcopy/pbpaste.
type MacClipboard struct{}

func (c *MacClipboard) Read() ([]byte, error) {
	cmd := exec.Command("pbpaste")
	return cmd.Output()
}

func (c *MacClipboard) Write(data []byte) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = bytes.NewReader(data)
	return cmd.Run()
}
