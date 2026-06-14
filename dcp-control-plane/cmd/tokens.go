package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/spf13/cobra"
)

var tokenCount int

var tokensCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage provider registration tokens",
}

var tokenGenCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate one-time provider registration tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		for i := 0; i < tokenCount; i++ {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			fmt.Println(hex.EncodeToString(b))
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tokensCmd)
	tokensCmd.AddCommand(tokenGenCmd)
	tokenGenCmd.Flags().IntVarP(&tokenCount, "count", "n", 1, "Number of tokens to generate")
}
