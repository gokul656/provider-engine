package cmd

import (
	"log/slog"
	"os"

	"github.com/dcp/control-plane/internal/api"
	"github.com/spf13/cobra"
)

var (
	listenAddr string
	redisAddr  string
	pgDSN      string
	sshCAPath  string
	wgPrivKey  string
	wgPubKey   string
	jwtSecret  string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the control plane HTTP API server",
	RunE: func(cmd *cobra.Command, args []string) error {
		setupLogger("info")
		cfg := api.Config{
			ListenAddr: listenAddr,
			RedisAddr:  redisAddr,
			PGDSN:      pgDSN,
			SSHCAPath:  sshCAPath,
			WGPrivKey:  wgPrivKey,
			WGPubKey:   wgPubKey,
			JWTSecret:  jwtSecret,
		}
		slog.Info("control plane starting", "addr", listenAddr)
		return api.Serve(cfg)
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().StringVar(&listenAddr, "addr", envOr("DCP_LISTEN", ":8080"), "HTTP listen address")
	serveCmd.Flags().StringVar(&redisAddr, "redis", envOr("DCP_REDIS", "redis://localhost:6379"), "Redis URL")
	serveCmd.Flags().StringVar(&pgDSN, "pg", envOr("DCP_PG_DSN", "postgres://dcp:dcp@localhost/dcp_registry?sslmode=disable"), "PostgreSQL DSN")
	serveCmd.Flags().StringVar(&sshCAPath, "ssh-ca", envOr("DCP_SSH_CA", "/etc/dcp/ssh-ca/platform_ca"), "SSH CA private key path")
	serveCmd.Flags().StringVar(&wgPrivKey, "wg-privkey", envOr("DCP_WG_PRIVKEY", "/etc/wireguard/cp_private"), "WireGuard server private key path")
	serveCmd.Flags().StringVar(&wgPubKey, "wg-pubkey", envOr("DCP_WG_PUBKEY", "/etc/wireguard/cp_public"), "WireGuard server public key path")
	serveCmd.Flags().StringVar(&jwtSecret, "jwt-secret", envOr("DCP_JWT_SECRET", ""), "JWT signing secret")
	_ = serveCmd.MarkFlagRequired("jwt-secret")
}

func setupLogger(level string) {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
}
