package clipx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundtrip(t *testing.T) {
	// use a temp dir
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// verify default is empty
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load empty config: %v", err)
	}
	if len(cfg.Peers) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(cfg.Peers))
	}

	// save with peers
	cfg.Peers = []string{"192.168.1.10", "192.168.1.20"}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	// verify file exists
	path := filepath.Join(tmpDir, ".config", "clipx", "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// reload and verify
	cfg2, err := LoadConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg2.Peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(cfg2.Peers))
	}
	if cfg2.Peers[0] != "192.168.1.10" || cfg2.Peers[1] != "192.168.1.20" {
		t.Errorf("peers mismatch: %v", cfg2.Peers)
	}
}

func TestResolveAddr(t *testing.T) {
	// IP passthrough
	ip, err := ResolveAddr("192.168.1.1")
	if err != nil {
		t.Fatalf("resolve IP: %v", err)
	}
	if ip != "192.168.1.1" {
		t.Errorf("got %s, want 192.168.1.1", ip)
	}

	// localhost should resolve
	ip, err = ResolveAddr("localhost")
	if err != nil {
		t.Fatalf("resolve localhost: %v", err)
	}
	if ip != "127.0.0.1" {
		t.Errorf("got %s, want 127.0.0.1", ip)
	}

	// nonsense should fail
	_, err = ResolveAddr("this.host.definitely.does.not.exist.invalid")
	if err == nil {
		t.Error("expected error for nonexistent host")
	}
}
