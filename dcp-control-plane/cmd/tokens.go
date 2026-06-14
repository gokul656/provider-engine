package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/dcp/control-plane/internal/setup"
	"github.com/dcp/control-plane/pkg/store"
	"github.com/spf13/cobra"
)

var (
	tokenCount     int
	withSpireToken bool
	tokenRedisAddr string
)

var tokensCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage provider registration tokens",
}

var tokenGenCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate provider registration token(s)",
	Long: `Generates a DCP registration token (for --token flag on dcp-provider)
and stores it in Redis so the control plane will accept it at registration.
Use --spire to also generate a SPIRE join token the provider needs for SVID issuance.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		// Connect to Redis so generated tokens are persisted and accepted on register.
		rdb, err := store.NewRedis(tokenRedisAddr)
		if err != nil {
			return fmt.Errorf("connect redis (is it running? --redis %s): %w", tokenRedisAddr, err)
		}

		for i := 0; i < tokenCount; i++ {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			tok := hex.EncodeToString(b)

			if err := rdb.AddToken(ctx, tok); err != nil {
				return fmt.Errorf("store token: %w", err)
			}
			fmt.Printf("DCP_TOKEN=%s\n", tok)

			if withSpireToken {
				spireToken, err := setup.GenerateProviderJoinToken(ctx)
				if err != nil {
					fmt.Printf("  SPIRE_JOIN_TOKEN=<error: %v — is spire-server running?>\n", err)
				} else {
					fmt.Printf("SPIRE_JOIN_TOKEN=%s\n", spireToken)
				}
			}

			if i < tokenCount-1 {
				fmt.Println()
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tokensCmd)
	tokensCmd.AddCommand(tokenGenCmd)
	tokenGenCmd.Flags().IntVarP(&tokenCount, "count", "n", 1, "Number of token pairs to generate")
	tokenGenCmd.Flags().BoolVar(&withSpireToken, "spire", false, "Also generate a SPIRE join token for each provider")
	tokenGenCmd.Flags().StringVar(&tokenRedisAddr, "redis", envOr("DCP_REDIS", "redis://localhost:6379"), "Redis URL (must match the serve command)")
}
