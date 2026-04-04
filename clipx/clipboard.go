package clipx

import (
	"os/exec"
	"strings"
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
	cmd.Stdin = strings.NewReader(string(data))
	return cmd.Run()
}
