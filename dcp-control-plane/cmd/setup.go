package cmd

import (
	"github.com/dcp/control-plane/internal/setup"
	"github.com/spf13/cobra"
)

var (
	setupWGEndpoint string
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure this machine as a DCP control plane (run once as root)",
	Long: `Idempotent first-boot configurator. Handles:
  • SSH CA key generation
  • WireGuard keypair + wg0 interface
  • SPIRE server config, systemd unit, join token
  • SPIRE agent config + systemd unit
  • PostgreSQL user/database
  • JWT secret generation
  • dcp-cp systemd service unit
  • UFW firewall rules`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return setup.Run(setup.Config{
			WGEndpoint: setupWGEndpoint,
			SSHCAPath:  "/etc/dcp/ssh-ca/platform_ca",
			WGPrivPath: "/etc/wireguard/cp_private",
			WGPubPath:  "/etc/wireguard/cp_public",
		})
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
	setupCmd.Flags().StringVar(&setupWGEndpoint,
		"wg-endpoint",
		envOr("DCP_PUBLIC_IP", "127.0.0.1")+":51820",
		"Public host:port providers use to reach this machine's WireGuard (e.g. 1.2.3.4:51820)")
}
