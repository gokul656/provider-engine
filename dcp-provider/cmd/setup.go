package cmd

import (
	"github.com/dcp/provider-agent/internal/setup"
	"github.com/spf13/cobra"
)

var (
	setupCPIP       string
	setupSpireToken string
	setupCPURL      string
	setupToken      string
	setupLocation   string
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure this machine as a DCP provider (run once as root)",
	Long: `Idempotent first-boot configurator. Handles:
  • WireGuard keypair generation (prov_private / prov_public)
  • SPIRE agent config + systemd unit
  • /etc/dcp/provider.env
  • dcp-provider systemd service unit
  • UFW firewall rules`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return setup.Run(setup.Config{
			CPIP:       setupCPIP,
			SpireToken: setupSpireToken,
			CPURL:      setupCPURL,
			Token:      setupToken,
			Location:   setupLocation,
		})
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
	setupCmd.Flags().StringVar(&setupCPIP, "cp-ip", envOr("DCP_CP_IP", "127.0.0.1"), "Control plane IP (for SPIRE agent)")
	setupCmd.Flags().StringVar(&setupSpireToken, "spire-token", envOr("SPIRE_JOIN_TOKEN", ""), "SPIRE join token from `dcp-cp token generate --spire`")
	setupCmd.Flags().StringVar(&setupCPURL, "cp-url", envOr("DCP_CP_URL", ""), "Control plane URL (e.g. https://1.2.3.4:8080)")
	setupCmd.Flags().StringVar(&setupToken, "token", envOr("DCP_TOKEN", ""), "DCP registration token from `dcp-cp token generate`")
	setupCmd.Flags().StringVar(&setupLocation, "location", envOr("DCP_LOCATION", "unknown"), "Location tag (e.g. us-east)")
	_ = setupCmd.MarkFlagRequired("cp-url")
	_ = setupCmd.MarkFlagRequired("token")
}
