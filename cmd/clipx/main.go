package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/gomantics/clipx/internal/clipx"
)

var version = "dev"

func getVersion() string {
	if version != "dev" && version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("clipx %s\n", getVersion())
			return
		case "install":
			cmdInstall()
			return
		case "uninstall":
			cmdUninstall()
			return
		case "pair":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: clipx pair <ip-or-hostname>")
				os.Exit(1)
			}
			cmdPair(os.Args[2])
			return
		case "unpair":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: clipx unpair <ip-or-hostname>")
				os.Exit(1)
			}
			cmdUnpair(os.Args[2])
			return
		case "peers":
			cmdPeers()
			return
		case "status":
			cmdStatus()
			return
		case "update", "self-update":
			cmdUpdate()
			return
		case "help", "--help", "-h":
			printUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\nrun 'clipx help' for usage\n", os.Args[1])
			os.Exit(1)
		}
	}

	cmdRun()
}

func printUsage() {
	fmt.Printf(`clipx %s — LAN clipboard sync for macOS

Usage:
  clipx                       Start the clipboard sync daemon
  clipx pair <ip|hostname>    Add a peer to sync with
  clipx unpair <ip|hostname>  Remove a peer
  clipx peers                 List paired peers and their status
  clipx install               Install as LaunchAgent + allow through firewall
  clipx uninstall             Remove LaunchAgent
  clipx status                Show daemon status and recent logs
  clipx update                Self-update to latest release
  clipx version               Print version
  clipx help                  Show this help

Setup:
  1. Install clipx on both Macs
  2. Run 'clipx pair <other-mac-ip>' on each Mac
  3. Run 'clipx install' on each Mac (runs at login, allows firewall)
  4. Done — copy on one Mac, paste on the other
`, getVersion())
}

// --- Commands ---

func cmdRun() {
	logger := log.New(os.Stdout, "[clipx] ", log.LstdFlags|log.Lmsgprefix)

	cfg, err := clipx.LoadConfig()
	if err != nil {
		logger.Printf("warning: config: %v (starting with no peers)", err)
		cfg = &clipx.Config{}
	}

	if len(cfg.Peers) == 0 {
		logger.Println("no peers configured — run 'clipx pair <ip>' to add one")
	}

	node, err := clipx.NewNode(cfg, logger)
	if err != nil {
		logger.Fatalf("failed to start: %v", err)
	}

	logger.Printf("starting clipx %s", getVersion())
	logger.Printf("listening on udp :%d", clipx.DefaultPort)
	for _, p := range cfg.Peers {
		logger.Printf("peer: %s", p)
	}

	node.Start()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	node.Stop()
	logger.Println("stopped")
}

func cmdPair(addr string) {
	resolved, err := clipx.ResolveAddr(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot resolve %s: %v\n", addr, err)
		os.Exit(1)
	}

	cfg, err := clipx.LoadConfig()
	if err != nil {
		cfg = &clipx.Config{}
	}

	for _, p := range cfg.Peers {
		if p == resolved {
			fmt.Printf("already paired with %s\n", resolved)
			return
		}
	}

	cfg.Peers = append(cfg.Peers, resolved)
	if err := clipx.SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ paired with %s\n", resolved)
	restartIfRunning()
}

func cmdUnpair(addr string) {
	resolved, err := clipx.ResolveAddr(addr)
	if err != nil {
		resolved = addr // try raw match
	}

	cfg, err := clipx.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	found := false
	var remaining []string
	for _, p := range cfg.Peers {
		if p == resolved || p == addr {
			found = true
		} else {
			remaining = append(remaining, p)
		}
	}

	if !found {
		fmt.Fprintf(os.Stderr, "peer %s not found\n", addr)
		os.Exit(1)
	}

	cfg.Peers = remaining
	if err := clipx.SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ unpaired from %s\n", addr)
	restartIfRunning()
}

func cmdPeers() {
	cfg, err := clipx.LoadConfig()
	if err != nil || len(cfg.Peers) == 0 {
		fmt.Println("no peers configured")
		fmt.Println("run 'clipx pair <ip>' to add one")
		return
	}

	fmt.Printf("peers (%d):\n", len(cfg.Peers))
	for _, p := range cfg.Peers {
		status := clipx.PingPeer(p)
		fmt.Printf("  %s  %s\n", status, p)
	}
}

func cmdStatus() {
	out, err := exec.Command("launchctl", "list", launchAgentLabel).Output()
	if err != nil {
		fmt.Println("○ clipx is not running as a LaunchAgent")
	} else {
		fmt.Println("● clipx is running as a LaunchAgent")
		fmt.Println(string(out))
	}

	logPath := logFilePath()
	fmt.Printf("  logs: %s\n", logPath)
	fmt.Printf("  config: %s\n", clipx.ConfigPath())

	cfg, _ := clipx.LoadConfig()
	if cfg != nil && len(cfg.Peers) > 0 {
		fmt.Printf("  peers: %s\n", strings.Join(cfg.Peers, ", "))
	}

	fmt.Println("\nRecent log:")
	tail, _ := exec.Command("tail", "-20", logPath).Output()
	if len(tail) > 0 {
		fmt.Print(string(tail))
	} else {
		fmt.Println("  (no logs yet)")
	}
}

// detectInstallMethod returns "brew", "go", or "binary" based on
// where the current clipx binary lives.
func detectInstallMethod() string {
	exePath, err := os.Executable()
	if err != nil {
		return "binary"
	}
	exePath, _ = filepath.EvalSymlinks(exePath)

	if strings.Contains(exePath, "Cellar") || strings.Contains(exePath, "homebrew") {
		return "brew"
	}

	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, _ := os.UserHomeDir()
		gopath = filepath.Join(home, "go")
	}
	if strings.HasPrefix(exePath, filepath.Join(gopath, "bin")) {
		return "go"
	}

	return "binary"
}

func cmdUpdate() {
	current := getVersion()
	fmt.Printf("current version: %s\n", current)

	method := detectInstallMethod()

	switch method {
	case "brew":
		fmt.Println("installed via Homebrew, updating...")
		cmd := exec.Command("brew", "upgrade", "clipx")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "error: brew upgrade failed: %v\n", err)
			os.Exit(1)
		}

	case "go":
		fmt.Println("installed via go install, updating...")
		cmd := exec.Command("go", "install", "github.com/gomantics/clipx/cmd/clipx@latest")
		cmd.Env = append(os.Environ(),
			"GONOSUMDB=github.com/gomantics/clipx",
			"GONOSUMCHECK=github.com/gomantics/clipx",
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "error: go install failed: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintln(os.Stderr, "cannot detect install method")
		fmt.Fprintln(os.Stderr, "update manually from https://github.com/gomantics/clipx/releases")
		os.Exit(1)
	}

	fmt.Println("✓ clipx updated")
	restartIfRunning()
}

// restartIfRunning restarts the LaunchAgent if it's currently loaded.
func restartIfRunning() {
	plistPath := launchAgentPath()
	if _, err := os.Stat(plistPath); err != nil {
		return // not installed as LaunchAgent
	}
	if err := exec.Command("launchctl", "list", launchAgentLabel).Run(); err != nil {
		return // not currently running
	}
	fmt.Println("restarting LaunchAgent...")
	exec.Command("launchctl", "unload", plistPath).Run()
	time.Sleep(500 * time.Millisecond)
	exec.Command("launchctl", "load", plistPath).Run()
	fmt.Println("✓ LaunchAgent restarted")
}

// --- LaunchAgent ---

const launchAgentLabel = "com.gomantics.clipx"

var launchAgentPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
    <key>ProcessType</key>
    <string>Background</string>
</dict>
</plist>
`

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

func logFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "clipx.log")
}

func cmdInstall() {
	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine binary path: %v\n", err)
		os.Exit(1)
	}
	binPath, _ = filepath.EvalSymlinks(binPath)

	// add firewall exception
	fmt.Println("adding firewall exception (may require sudo password)...")
	fwTool := "/usr/libexec/ApplicationFirewall/socketfilterfw"
	if _, err := os.Stat(fwTool); err == nil {
		err1 := exec.Command("sudo", fwTool, "--add", binPath).Run()
		err2 := exec.Command("sudo", fwTool, "--unblockapp", binPath).Run()
		if err1 != nil || err2 != nil {
			fmt.Println("⚠ firewall exception failed — you may need to allow clipx manually")
		} else {
			fmt.Println("✓ firewall exception added")
		}
	}

	plistPath := launchAgentPath()
	logPath := logFilePath()

	tmpl, _ := template.New("plist").Parse(launchAgentPlist)

	f, err := os.Create(plistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot create %s: %v\n", plistPath, err)
		os.Exit(1)
	}
	defer f.Close()

	data := struct{ Label, BinaryPath, LogPath string }{
		launchAgentLabel, binPath, logPath,
	}
	if err := tmpl.Execute(f, data); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	exec.Command("launchctl", "unload", plistPath).Run()
	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: launchctl load failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ clipx installed and started")
	fmt.Printf("  binary: %s\n", binPath)
	fmt.Printf("  plist:  %s\n", plistPath)
	fmt.Printf("  logs:   %s\n", logPath)
	fmt.Printf("  config: %s\n", clipx.ConfigPath())
	fmt.Println("\nclipx will start automatically at login.")
}

func cmdUninstall() {
	plistPath := launchAgentPath()
	exec.Command("launchctl", "unload", plistPath).Run()

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: cannot remove %s: %v\n", plistPath, err)
		os.Exit(1)
	}

	fmt.Println("✓ clipx LaunchAgent removed")
}
