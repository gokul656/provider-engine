package wireguard

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)


const (
	iface      = "wg0"
	configPath = "/etc/wireguard/wg0.conf"
	privPath   = "/etc/wireguard/prov_private"
	pubPath    = "/etc/wireguard/prov_public"
)

// GenerateKeypair generates a WireGuard keypair and writes both keys to
// /etc/wireguard/prov_private and /etc/wireguard/prov_public, matching
// the paths created by wireguard.sh. Returns the public key string.
func GenerateKeypair() (privKeyB64, pubKeyB64 string, err error) {
	if err = os.MkdirAll("/etc/wireguard", 0700); err != nil {
		return
	}

	// If keys already exist on disk, reuse them (idempotent across restarts).
	if existing, e := os.ReadFile(pubPath); e == nil {
		priv, _ := os.ReadFile(privPath)
		privKeyB64 = strings.TrimSpace(string(priv))
		pubKeyB64 = strings.TrimSpace(string(existing))
		return
	}

	priv := make([]byte, 32)
	if _, err = rand.Read(priv); err != nil {
		return
	}
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64

	privKeyB64 = base64.StdEncoding.EncodeToString(priv)

	cmd := exec.Command("wg", "pubkey")
	cmd.Stdin = strings.NewReader(privKeyB64 + "\n")
	out, e := cmd.Output()
	if e != nil {
		err = fmt.Errorf("wg pubkey: %w", e)
		return
	}
	pubKeyB64 = strings.TrimSpace(string(out))

	if err = os.WriteFile(privPath, []byte(privKeyB64+"\n"), 0600); err != nil {
		return
	}
	if err = os.WriteFile(pubPath, []byte(pubKeyB64+"\n"), 0644); err != nil {
		return
	}
	return
}

// WriteConfig writes the wg1.conf received from the control plane and brings
// up the wg1 interface. CP always owns wg0; provider always owns wg1.
func WriteConfig(conf string) error {
	if err := os.MkdirAll("/etc/wireguard", 0700); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(conf), 0600); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	slog.Info("wireguard: wrote config", "path", configPath)
	return bringUp()
}

func bringUp() error {
	if isUp() {
		slog.Info("wireguard: syncing existing interface", "iface", iface)
		out, err := exec.Command("wg", "syncconf", iface, configPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("wg syncconf: %w: %s", err, out)
		}
		return nil
	}
	slog.Info("wireguard: bringing up interface", "iface", iface)
	out, err := exec.Command("wg-quick", "up", iface).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg-quick up %s: %w: %s", iface, err, out)
	}
	return nil
}

func isUp() bool {
	return exec.Command("ip", "link", "show", iface).Run() == nil
}

