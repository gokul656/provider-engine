package wireguard

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

const configPath = "/etc/wireguard/wg0.conf"

// GenerateKeypair returns (privateKey, publicKey) as base64 strings.
func GenerateKeypair() (string, string, error) {
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return "", "", err
	}
	// Clamp per RFC
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64

	// Use wg command to derive pubkey — avoids reimplementing curve25519 scalar mult
	cmd := exec.Command("wg", "pubkey")
	cmd.Stdin = newBase64Reader(priv)
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("wg pubkey: %w", err)
	}
	privB64 := base64.StdEncoding.EncodeToString(priv)
	pubB64 := string(out[:len(out)-1]) // strip newline
	return privB64, pubB64, nil
}

// WriteConfig writes the wg0.conf received from the control plane
// and brings up the interface.
func WriteConfig(conf string) error {
	if err := os.MkdirAll("/etc/wireguard", 0700); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(conf), 0600); err != nil {
		return fmt.Errorf("write wg0.conf: %w", err)
	}
	slog.Info("wireguard: wrote config", "path", configPath)
	return bringUp()
}

func bringUp() error {
	// If already up, sync; otherwise start fresh.
	if isUp() {
		slog.Info("wireguard: syncing existing interface")
		out, err := exec.Command("wg", "syncconf", "wg0", configPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("wg syncconf: %w: %s", err, out)
		}
		return nil
	}
	slog.Info("wireguard: bringing up wg0")
	out, err := exec.Command("wg-quick", "up", "wg0").CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg-quick up: %w: %s", err, out)
	}
	return nil
}

func isUp() bool {
	return exec.Command("ip", "link", "show", "wg0").Run() == nil
}

func newBase64Reader(b []byte) *os.File {
	// Write priv key as base64 to a temp file so we can pipe it
	f, _ := os.CreateTemp("", "wgkey")
	f.WriteString(base64.StdEncoding.EncodeToString(b) + "\n")
	f.Seek(0, 0)
	return f
}
