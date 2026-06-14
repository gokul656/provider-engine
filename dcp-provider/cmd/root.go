package cmd

import (
	"fmt"
	"os"

	"github.com/dcp/provider-agent/internal/agent"
	"github.com/spf13/cobra"
)

var (
	cpURL    string
	token    string
	location string
	logLevel string
	mode     string
	sshPort  int
)

var rootCmd = &cobra.Command{
	Use:   "dcp-provider",
	Short: "DCP Provider Agent — register this machine as a compute provider",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := agent.Config{
			ControlPlaneURL: cpURL,
			Token:           token,
			Location:        location,
			LogLevel:        logLevel,
			Mode:            mode,
			SSHPort:         sshPort,
		}
		return agent.Run(cfg)
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().StringVar(&cpURL, "cp-url", envOr("DCP_CP_URL", "https://api.dcp.internal"), "Control plane base URL")
	rootCmd.Flags().StringVar(&token, "token", envOr("DCP_TOKEN", ""), "Provider registration token")
	rootCmd.Flags().StringVar(&location, "location", envOr("DCP_LOCATION", "us-east"), "Provider location tag")
	rootCmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level (debug|info|warn|error)")
	rootCmd.Flags().StringVar(&mode, "mode", envOr("DCP_MODE", "auto"), "Mode: auto|firecracker|container|ssh (ssh = Termux/no-root)")
	rootCmd.Flags().IntVar(&sshPort, "ssh-port", 0, "SSH port to advertise in ssh mode (default: 8022 on Android, 22 otherwise)")
	_ = rootCmd.MarkFlagRequired("token")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
