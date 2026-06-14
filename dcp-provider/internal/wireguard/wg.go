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


const iface = "wg0"

// paths returns config paths appropriate for the current environment.
// On Android/Termux (no root) everything lives under $HOME/.dcp/wireguard/.
func paths() (cfgPath, priv, pub string) {
	if isTermux() {
		base := os.Getenv("HOME") + "/.dcp/wireguard"
		return base + "/wg0.conf", base + "/prov_private", base + "/prov_public"
	}
	return "/etc/wireguard/wg0.conf", "/etc/wireguard/prov_private", "/etc/wireguard/prov_public"
}

func isTermux() bool {
	return os.Getenv("ANDROID_ROOT") != ""
}

// GenerateKeypair generates a WireGuard keypair and writes both keys to
// /etc/wireguard/prov_private and /etc/wireguard/prov_public, matching
// the paths created by wireguard.sh. Returns the public key string.
func GenerateKeypair() (privKeyB64, pubKeyB64 string, err error) {
	cfgPath, privPath, pubPath := paths()
	if err = os.MkdirAll(cfgPath[:strings.LastIndex(cfgPath, "/")], 0700); err != nil {
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

// WriteConfig writes the wg0.conf received from the control plane and brings
// up the interface. Uses wireguard-go (userspace) on Termux, wg-quick elsewhere.
func WriteConfig(conf string) error {
	cfgPath, _, _ := paths()
	dir := cfgPath[:strings.LastIndex(cfgPath, "/")]
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, []byte(conf), 0600); err != nil {
		return fmt.Errorf("write %s: %w", cfgPath, err)
	}
	slog.Info("wireguard: wrote config", "path", cfgPath)
	return bringUp()
}

func bringUp() error {
	cfgPath, _, _ := paths()
	if isTermux() {
		return bringUpUserspace(cfgPath)
	}
	if isUp() {
		slog.Info("wireguard: syncing existing interface", "iface", iface)
		out, err := exec.Command("wg", "syncconf", iface, cfgPath).CombinedOutput()
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

// bringUpUserspace uses wireguard-go (no root, no kernel module).
// On Termux: pkg install wireguard-go
func bringUpUserspace(cfgPath string) error {
	// Kill any existing instance
	exec.Command("pkill", "-f", "wireguard-go "+iface).Run()

	slog.Info("wireguard: starting wireguard-go (userspace)", "iface", iface)
	cmd := exec.Command("wireguard-go", iface)
	cmd.Env = append(os.Environ(), "WG_QUICK_USERSPACE_IMPLEMENTATION=wireguard-go")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("wireguard-go: %w: %s", err, out)
	}

	// Apply config via wg setconf
	out, err := exec.Command("wg", "setconf", iface, cfgPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg setconf: %w: %s", err, out)
	}
	return nil
}

func isUp() bool {
	return exec.Command("ip", "link", "show", iface).Run() == nil
}

