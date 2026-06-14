package setup

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

type Config struct {
	CPIP        string // IP or hostname of control plane (for SPIRE agent)
	SpireToken  string // join token from `dcp-cp token generate --spire`
	CPURL       string // e.g. https://1.2.3.4:8080
	Token       string // DCP registration token
	Location    string
}

const trustDomain = "dcp.io"

// Run executes all provider setup steps in order.
func Run(cfg Config) error {
	steps := []struct {
		name string
		fn   func(Config) error
	}{
		{"WireGuard keypair", setupWGKeypair},
		{"SPIRE agent", setupSpireAgent},
		{"env file", writeEnvFile},
		{"systemd: dcp-provider", writeSystemdUnit},
		{"firewall", setupFirewall},
	}

	for _, s := range steps {
		slog.Info("setup", "step", s.name)
		if err := s.fn(cfg); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}

	printSummary(cfg)
	return nil
}

// ── WireGuard keypair ─────────────────────────────────────────────────────────

func setupWGKeypair(_ Config) error {
	if err := os.MkdirAll("/etc/wireguard", 0700); err != nil {
		return err
	}
	privPath := "/etc/wireguard/prov_private"
	pubPath  := "/etc/wireguard/prov_public"

	if fileExists(privPath) {
		slog.Info("setup: WireGuard keypair already exists, skipping")
		return nil
	}

	priv, err := runOutput("wg", "genkey")
	if err != nil {
		return fmt.Errorf("wg genkey: %w", err)
	}
	pub, err := runOutputStdin(strings.TrimSpace(priv), "wg", "pubkey")
	if err != nil {
		return fmt.Errorf("wg pubkey: %w", err)
	}

	if err := writeFile(privPath, priv, 0600); err != nil {
		return err
	}
	if err := writeFile(pubPath, pub, 0644); err != nil {
		return err
	}
	slog.Info("setup: WireGuard keypair written", "pubkey", strings.TrimSpace(pub))
	fmt.Printf("\n  Provider WireGuard public key: %s\n", strings.TrimSpace(pub))
	fmt.Println("  The control plane will add this as a peer on registration.")
	return nil
}

// ── SPIRE agent ───────────────────────────────────────────────────────────────

func setupSpireAgent(cfg Config) error {
	os.MkdirAll("/var/lib/spire/agent", 0700)
	os.MkdirAll("/tmp/spire-agent/public", 0755)
	os.MkdirAll("/etc/spire", 0755)

	cpIP := cfg.CPIP
	if cpIP == "" {
		cpIP = "127.0.0.1"
	}

	conf := fmt.Sprintf(`agent {
  data_dir       = "/var/lib/spire/agent"
  log_level      = "INFO"
  server_address = "%s"
  server_port    = "8081"
  trust_domain   = "%s"
  socket_path    = "/tmp/spire-agent/public/api.sock"
}
plugins {
  NodeAttestor     "join_token" {}
  KeyManager       "memory" {}
  WorkloadAttestor "unix" {}
}
`, cpIP, trustDomain)

	if err := writeFile("/etc/spire/agent.conf", conf, 0644); err != nil {
		return err
	}

	// Write join token if provided
	if cfg.SpireToken != "" {
		if err := writeFile("/etc/spire/join_token", cfg.SpireToken+"\n", 0600); err != nil {
			return err
		}
		slog.Info("setup: SPIRE join token saved")
	} else {
		slog.Info("setup: no SPIRE token provided — set it in /etc/spire/join_token before starting agent")
	}

	unit := `[Unit]
Description=SPIRE Agent
After=network.target

[Service]
ExecStartPre=/bin/mkdir -p /tmp/spire-agent/public
ExecStart=/bin/bash -c '/usr/local/bin/spire-agent run \
  -config /etc/spire/agent.conf \
  -joinToken $(cat /etc/spire/join_token)'
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`
	if err := writeFile("/etc/systemd/system/spire-agent.service", unit, 0644); err != nil {
		return err
	}
	run("systemctl", "daemon-reload")
	run("systemctl", "enable", "spire-agent")

	if cfg.SpireToken != "" {
		run("systemctl", "start", "spire-agent")
	}
	return nil
}

// ── Env file ──────────────────────────────────────────────────────────────────

func writeEnvFile(cfg Config) error {
	os.MkdirAll("/etc/dcp", 0700)
	envPath := "/etc/dcp/provider.env"

	content := fmt.Sprintf("DCP_CP_URL=%s\nDCP_TOKEN=%s\nDCP_LOCATION=%s\n",
		cfg.CPURL, cfg.Token, cfg.Location)
	if err := writeFile(envPath, content, 0600); err != nil {
		return err
	}
	slog.Info("setup: env file written", "path", envPath)
	return nil
}

// ── Systemd unit ──────────────────────────────────────────────────────────────

func writeSystemdUnit(_ Config) error {
	unit := `[Unit]
Description=DCP Provider Agent
After=network.target spire-agent.service

[Service]
EnvironmentFile=/etc/dcp/provider.env
ExecStart=/usr/local/bin/dcp-provider \
  --cp-url ${DCP_CP_URL} \
  --token ${DCP_TOKEN} \
  --location ${DCP_LOCATION}
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`
	if err := writeFile("/etc/systemd/system/dcp-provider.service", unit, 0644); err != nil {
		return err
	}
	run("systemctl", "daemon-reload")
	run("systemctl", "enable", "dcp-provider")
	slog.Info("setup: dcp-provider.service written and enabled")
	return nil
}

// ── Firewall ──────────────────────────────────────────────────────────────────

func setupFirewall(_ Config) error {
	run("ufw", "allow", "22/tcp")
	run("ufw", "allow", "51821/udp") // WireGuard client port
	run("ufw", "--force", "enable")
	slog.Info("setup: firewall rules applied")
	return nil
}

// ── Summary ───────────────────────────────────────────────────────────────────

func printSummary(cfg Config) {
	pub, _ := os.ReadFile("/etc/wireguard/prov_public")
	fmt.Println()
	fmt.Println("────────────────────────────────────────────────────")
	fmt.Println(" DCP Provider setup complete")
	fmt.Println("────────────────────────────────────────────────────")
	fmt.Printf(" WG pubkey:   %s", string(pub))
	fmt.Printf(" CP URL:      %s\n", cfg.CPURL)
	fmt.Printf(" Location:    %s\n", cfg.Location)
	fmt.Printf(" Env file:    /etc/dcp/provider.env\n")
	fmt.Println()
	fmt.Println(" Next steps:")
	fmt.Println("   1. Copy binary:   cp dcp-provider /usr/local/bin/dcp-provider")
	fmt.Println("   2. Start service: systemctl start dcp-provider")
	fmt.Println("   3. Watch logs:    journalctl -u dcp-provider -f")
	fmt.Println("────────────────────────────────────────────────────")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func fileExists(p string) bool        { _, err := os.Stat(p); return err == nil }
func writeFile(p, c string, m os.FileMode) error { return os.WriteFile(p, []byte(c), m) }
func run(n string, a ...string)       { exec.Command(n, a...).Run() }

func runOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func runOutputStdin(stdin, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	return string(out), err
}
