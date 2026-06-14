package wgrelay

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

const configPath = "/etc/wireguard/wg0.conf"

// Config holds the CP-side WireGuard config.
type Config struct {
	PrivKeyPath string
	PubKeyPath  string
	ListenPort  int
	Address     string // e.g. "10.99.0.1/24"
}

// Init writes the CP wg0.conf (if not present) and brings up the interface.
func Init(cfg Config) error {
	privKey, err := os.ReadFile(cfg.PrivKeyPath)
	if err != nil {
		return fmt.Errorf("read wg privkey: %w", err)
	}

	if _, err := os.Stat(configPath); err == nil {
		slog.Info("wgrelay: wg0.conf already exists, syncing")
		return syncUp()
	}

	conf := fmt.Sprintf(`[Interface]
Address = %s
ListenPort = %d
PrivateKey = %s
`, cfg.Address, cfg.ListenPort, strings.TrimSpace(string(privKey)))

	if err := os.WriteFile(configPath, []byte(conf), 0600); err != nil {
		return fmt.Errorf("write wg0.conf: %w", err)
	}
	slog.Info("wgrelay: wrote wg0.conf, bringing up interface")
	return bringUp()
}

// AddPeer adds a provider as a WireGuard peer.
func AddPeer(pubKey, allowedIP string) error {
	out, err := exec.Command("wg", "set", "wg0",
		"peer", pubKey,
		"allowed-ips", allowedIP+"/32",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg set peer: %w: %s", err, out)
	}
	// Persist to config
	return exec.Command("wg-quick", "save", "wg0").Run()
}

func bringUp() error {
	if isUp() {
		return syncUp()
	}
	out, err := exec.Command("wg-quick", "up", "wg0").CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg-quick up: %w: %s", err, out)
	}
	return nil
}

func syncUp() error {
	out, err := exec.Command("wg", "syncconf", "wg0", configPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg syncconf: %w: %s", err, out)
	}
	return nil
}

func isUp() bool {
	return exec.Command("ip", "link", "show", "wg0").Run() == nil
}

// PubKey reads the CP public key from disk.
func PubKey(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
