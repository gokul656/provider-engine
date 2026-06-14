package setup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	TrustDomain    = "dcp.io"
	SpireServerSock = "/tmp/spire-server/private/api.sock"
)

type Config struct {
	PGDSN      string // full DSN or empty (will build from password file)
	JWTSecret  string // empty = generate
	WGEndpoint string // host:port for providers to connect to, e.g. "1.2.3.4:51820"
	SSHCAPath  string
	WGPrivPath string
	WGPubPath  string
}

// Run executes all CP setup steps in order.
func Run(cfg Config) error {
	steps := []struct {
		name string
		fn   func(Config) error
	}{
		{"SSH CA", setupSSHCA},
		{"WireGuard (CP)", setupWireGuard},
		{"SPIRE server", setupSpireServer},
		{"SPIRE agent", setupSpireAgent},
		{"PostgreSQL", setupPostgres},
		{"JWT secret", setupJWTSecret},
		{"systemd: dcp-cp", writeSystemdUnit},
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

// ── SSH CA ────────────────────────────────────────────────────────────────────

func setupSSHCA(cfg Config) error {
	dir := "/etc/dcp/ssh-ca"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if fileExists(cfg.SSHCAPath) {
		slog.Info("setup: SSH CA already exists, skipping", "path", cfg.SSHCAPath)
		return nil
	}
	out, err := exec.Command("ssh-keygen",
		"-t", "ecdsa", "-b", "521",
		"-f", cfg.SSHCAPath,
		"-N", "",
		"-C", "dcp-platform-ssh-ca",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh-keygen: %w: %s", err, out)
	}
	os.Chmod(cfg.SSHCAPath, 0600)
	os.Chmod(cfg.SSHCAPath+".pub", 0644)
	slog.Info("setup: SSH CA generated", "path", cfg.SSHCAPath)
	return nil
}

// ── WireGuard ─────────────────────────────────────────────────────────────────

func setupWireGuard(cfg Config) error {
	if err := os.MkdirAll("/etc/wireguard", 0700); err != nil {
		return err
	}

	// Generate CP keypair if missing
	if !fileExists(cfg.WGPrivPath) {
		privKey, err := runOutput("wg", "genkey")
		if err != nil {
			return fmt.Errorf("wg genkey: %w", err)
		}
		pubKey, err := runOutputStdin(strings.TrimSpace(privKey), "wg", "pubkey")
		if err != nil {
			return fmt.Errorf("wg pubkey: %w", err)
		}
		if err := writeFile(cfg.WGPrivPath, privKey, 0600); err != nil {
			return err
		}
		if err := writeFile(cfg.WGPubPath, pubKey, 0644); err != nil {
			return err
		}
		slog.Info("setup: WireGuard keypair generated")
	}

	// Write wg0.conf if missing
	confPath := "/etc/wireguard/wg0.conf"
	if !fileExists(confPath) {
		privKey, err := os.ReadFile(cfg.WGPrivPath)
		if err != nil {
			return err
		}
		conf := fmt.Sprintf("[Interface]\nAddress = 10.99.0.1/16\nListenPort = 51820\nPrivateKey = %s\n",
			strings.TrimSpace(string(privKey)))
		if err := writeFile(confPath, conf, 0600); err != nil {
			return err
		}
		slog.Info("setup: wg0.conf written")
	}

	// Enable IP forwarding
	run("sysctl", "-w", "net.ipv4.ip_forward=1")
	appendIfMissing("/etc/sysctl.conf", "net.ipv4.ip_forward=1")

	// Bring up wg0
	if exec.Command("ip", "link", "show", "wg0").Run() != nil {
		if out, err := exec.Command("wg-quick", "up", "wg0").CombinedOutput(); err != nil {
			return fmt.Errorf("wg-quick up: %w: %s", err, out)
		}
	}
	run("systemctl", "enable", "wg-quick@wg0")
	slog.Info("setup: wg0 up")
	return nil
}

// ── SPIRE server ──────────────────────────────────────────────────────────────

func setupSpireServer(_ Config) error {
	if err := os.MkdirAll("/etc/spire", 0755); err != nil {
		return err
	}
	if err := os.MkdirAll("/var/lib/spire/server", 0700); err != nil {
		return err
	}

	confPath := "/etc/spire/server.conf"
	if !fileExists(confPath) {
		conf := fmt.Sprintf(`server {
  bind_address = "0.0.0.0"
  bind_port    = "8081"
  trust_domain = "%s"
  data_dir     = "/var/lib/spire/server"
  log_level    = "INFO"
  ca_ttl                = "24h"
  default_x509_svid_ttl = "1h"
}
plugins {
  DataStore "sql" {
    plugin_data {
      database_type     = "sqlite3"
      connection_string = "/var/lib/spire/server/datastore.sqlite3"
    }
  }
  KeyManager "disk" {
    plugin_data { keys_path = "/var/lib/spire/server/keys.json" }
  }
  NodeAttestor "join_token" {}
}
`, TrustDomain)
		if err := writeFile(confPath, conf, 0644); err != nil {
			return err
		}
	}

	unit := `[Unit]
Description=SPIRE Server
After=network.target

[Service]
ExecStart=/usr/local/bin/spire-server run -config /etc/spire/server.conf
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`
	if err := writeFile("/etc/systemd/system/spire-server.service", unit, 0644); err != nil {
		return err
	}
	run("systemctl", "daemon-reload")
	run("systemctl", "enable", "--now", "spire-server")

	// Wait up to 30s for server socket
	slog.Info("setup: waiting for SPIRE server socket...")
	for i := 0; i < 15; i++ {
		if fileExists(SpireServerSock) {
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Generate join token and save it
	token, err := runOutput("spire-server", "token", "generate",
		"-socketPath", SpireServerSock,
		"-spiffeID", fmt.Sprintf("spiffe://%s/node/cp", TrustDomain),
	)
	if err != nil {
		return fmt.Errorf("generate join token: %w", err)
	}
	// Output is "Token: <value>"
	parts := strings.Fields(token)
	if len(parts) < 2 {
		return fmt.Errorf("unexpected token output: %q", token)
	}
	tokenVal := parts[len(parts)-1]
	if err := os.MkdirAll("/etc/dcp", 0700); err != nil {
		return err
	}
	if err := writeFile("/etc/spire/join_token", tokenVal+"\n", 0600); err != nil {
		return err
	}
	slog.Info("setup: SPIRE server running, join token saved", "path", "/etc/spire/join_token")
	return nil
}

// ── SPIRE agent ───────────────────────────────────────────────────────────────

func setupSpireAgent(_ Config) error {
	os.MkdirAll("/var/lib/spire/agent", 0700)
	os.MkdirAll("/tmp/spire-agent/public", 0755)

	confPath := "/etc/spire/agent.conf"
	if !fileExists(confPath) {
		conf := fmt.Sprintf(`agent {
  data_dir       = "/var/lib/spire/agent"
  log_level      = "INFO"
  server_address = "127.0.0.1"
  server_port    = "8081"
  trust_domain   = "%s"
  socket_path    = "/tmp/spire-agent/public/api.sock"
}
plugins {
  NodeAttestor     "join_token" {}
  KeyManager       "memory" {}
  WorkloadAttestor "unix" {}
}
`, TrustDomain)
		if err := writeFile(confPath, conf, 0644); err != nil {
			return err
		}
	}

	unit := `[Unit]
Description=SPIRE Agent
After=network.target spire-server.service

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
	run("systemctl", "enable", "--now", "spire-agent")
	slog.Info("setup: SPIRE agent started")
	return nil
}

// ── PostgreSQL ────────────────────────────────────────────────────────────────

func setupPostgres(cfg Config) error {
	passFile := "/etc/dcp/pg_pass"
	os.MkdirAll("/etc/dcp", 0700)

	var pass string
	if fileExists(passFile) {
		b, _ := os.ReadFile(passFile)
		pass = strings.TrimSpace(string(b))
	} else {
		p, err := runOutput("openssl", "rand", "-hex", "24")
		if err != nil {
			return err
		}
		pass = strings.TrimSpace(p)
		if err := writeFile(passFile, pass+"\n", 0600); err != nil {
			return err
		}
	}

	// Create user + DB (idempotent)
	exec.Command("sudo", "-u", "postgres", "psql", "-c",
		fmt.Sprintf("CREATE USER dcp WITH PASSWORD '%s';", pass)).Run()
	exec.Command("sudo", "-u", "postgres", "psql", "-c",
		"CREATE DATABASE dcp_registry OWNER dcp;").Run()

	// Write DSN file so the systemd unit can read it
	dsn := fmt.Sprintf("postgres://dcp:%s@localhost/dcp_registry?sslmode=disable", pass)
	writeFile("/etc/dcp/pg_dsn", dsn+"\n", 0600)

	slog.Info("setup: PostgreSQL configured", "dsn_file", "/etc/dcp/pg_dsn")
	return nil
}

// ── JWT secret ────────────────────────────────────────────────────────────────

func setupJWTSecret(cfg Config) error {
	secretFile := "/etc/dcp/jwt_secret"
	if fileExists(secretFile) {
		return nil
	}
	secret, err := runOutput("openssl", "rand", "-hex", "32")
	if err != nil {
		return err
	}
	return writeFile(secretFile, strings.TrimSpace(secret)+"\n", 0600)
}

// ── systemd unit ──────────────────────────────────────────────────────────────

func writeSystemdUnit(cfg Config) error {
	unit := fmt.Sprintf(`[Unit]
Description=DCP Control Plane
After=network.target postgresql.service redis-server.service spire-agent.service wg-quick@wg0.service

[Service]
EnvironmentFile=/etc/dcp/pg_dsn
ExecStart=/usr/local/bin/dcp-cp serve \
  --jwt-secret $$(cat /etc/dcp/jwt_secret) \
  --pg $$(cat /etc/dcp/pg_dsn) \
  --redis redis://localhost:6379 \
  --ssh-ca %s \
  --wg-privkey %s \
  --wg-pubkey %s \
  --wg-endpoint %s
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`, cfg.SSHCAPath, cfg.WGPrivPath, cfg.WGPubPath, cfg.WGEndpoint)

	if err := writeFile("/etc/systemd/system/dcp-cp.service", unit, 0644); err != nil {
		return err
	}
	run("systemctl", "daemon-reload")
	slog.Info("setup: dcp-cp.service written")
	return nil
}

// ── Firewall ──────────────────────────────────────────────────────────────────

func setupFirewall(_ Config) error {
	for _, rule := range []string{"22/tcp", "443/tcp", "8080/tcp", "8081/tcp", "51820/udp"} {
		run("ufw", "allow", rule)
	}
	run("ufw", "--force", "enable")
	slog.Info("setup: firewall rules applied")
	return nil
}

// ── Summary ───────────────────────────────────────────────────────────────────

func printSummary(cfg Config) {
	dsn, _ := os.ReadFile("/etc/dcp/pg_dsn")
	fmt.Println()
	fmt.Println("────────────────────────────────────────────────────")
	fmt.Println(" DCP Control Plane setup complete")
	fmt.Println("────────────────────────────────────────────────────")
	fmt.Printf(" SSH CA:        %s\n", cfg.SSHCAPath)
	fmt.Printf(" WG pubkey:     %s\n", cfg.WGPubPath)
	fmt.Printf(" WG endpoint:   %s\n", cfg.WGEndpoint)
	fmt.Printf(" PG DSN:        %s\n", strings.TrimSpace(string(dsn)))
	fmt.Printf(" JWT secret:    /etc/dcp/jwt_secret\n")
	fmt.Printf(" SPIRE token:   /etc/spire/join_token\n")
	fmt.Println()
	fmt.Println(" Next steps:")
	fmt.Println("   1. Copy binary:   cp dcp-cp /usr/local/bin/dcp-cp")
	fmt.Println("   2. Start service: systemctl enable --now dcp-cp")
	fmt.Println("   3. Get token:     dcp-cp token generate")
	fmt.Println("────────────────────────────────────────────────────")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func writeFile(path, content string, mode os.FileMode) error {
	return os.WriteFile(path, []byte(content), mode)
}

func run(name string, args ...string) {
	exec.Command(name, args...).Run()
}

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

func appendIfMissing(path, line string) {
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), line) {
		f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
		defer f.Close()
		f.WriteString("\n" + line + "\n")
	}
}

// GenerateProviderJoinToken creates a new SPIRE join token for a provider machine.
// Called by dcp-cp token generate --spire.
func GenerateProviderJoinToken(ctx context.Context) (string, error) {
	out, err := runOutput("spire-server", "token", "generate",
		"-socketPath", SpireServerSock,
		"-spiffeID", fmt.Sprintf("spiffe://%s/node/provider", TrustDomain),
	)
	if err != nil {
		return "", fmt.Errorf("spire token: %w", err)
	}
	parts := strings.Fields(out)
	if len(parts) < 2 {
		return "", fmt.Errorf("unexpected output: %q", out)
	}
	return parts[len(parts)-1], nil
}
