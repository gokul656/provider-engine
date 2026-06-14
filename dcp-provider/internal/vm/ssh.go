package vm

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// SSHPassthrough represents the device itself as the compute endpoint.
// Used on Termux/Android where there are no VMs — the buyer SSHs directly
// into the provider device over the WireGuard tunnel.
type SSHPassthrough struct {
	JobID string
	ip    string
	port  int
}

type SSHConfig struct {
	JobID     string
	WireGuardIP string // assigned WG IP of this device, e.g. 10.99.1.1
	SSHPort   int    // sshd port (Termux default: 8022, Linux: 22)
	SSHPubKey string // buyer's public key to authorize
}

func SpawnSSH(ctx context.Context, cfg SSHConfig) (*SSHPassthrough, error) {
	port := cfg.SSHPort
	if port == 0 {
		port = detectSSHPort()
	}

	// Add the buyer's public key to authorized_keys
	if cfg.SSHPubKey != "" {
		if err := authorizeKey(cfg.SSHPubKey); err != nil {
			return nil, fmt.Errorf("authorize key: %w", err)
		}
	}

	slog.Info("ssh: passthrough ready", "job", cfg.JobID, "ip", cfg.WireGuardIP, "port", port)
	return &SSHPassthrough{
		JobID: cfg.JobID,
		ip:    cfg.WireGuardIP,
		port:  port,
	}, nil
}

func (s *SSHPassthrough) IP() string   { return s.ip }
func (s *SSHPassthrough) Port() int    { return s.port }
func (s *SSHPassthrough) Kill()        {} // nothing to kill — device keeps running

func detectSSHPort() int {
	// Termux sshd runs on 8022 by default; standard Linux on 22
	if exec.Command("getprop", "ro.build.version.sdk").Run() == nil {
		return 8022 // Android
	}
	return 22
}

// authorizeKey adds the buyer's public key to ~/.ssh/authorized_keys.
//
// The key is parsed and re-marshalled to its canonical form before being
// written — this both validates that it is a real SSH public key and
// guarantees the bytes hitting disk contain no shell metacharacters,
// injected commands, or extra authorized_keys options. All file operations
// use Go I/O; the key is never passed to a shell.
func authorizeKey(pubKey string) error {
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubKey))
	if err != nil {
		return fmt.Errorf("reject invalid SSH public key: %w", err)
	}
	// Canonical single-line form, e.g. "ssh-ed25519 AAAA...". No comment,
	// no options — exactly one key, nothing else.
	canonical := bytes.TrimSpace(ssh.MarshalAuthorizedKey(parsed))

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("mkdir ~/.ssh: %w", err)
	}
	akPath := filepath.Join(sshDir, "authorized_keys")

	// Skip if this exact key is already authorized.
	if existing, err := os.ReadFile(akPath); err == nil {
		for _, line := range bytes.Split(existing, []byte("\n")) {
			if bytes.Equal(bytes.TrimSpace(line), canonical) {
				return nil
			}
		}
	}

	f, err := os.OpenFile(akPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open authorized_keys: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(canonical, '\n')); err != nil {
		return fmt.Errorf("write authorized_keys: %w", err)
	}
	return nil
}
