package cmd

import (
	"context"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "saveany-bot",
	Short: "saveany-bot",
	Run:   Run,
	Args:  cobra.NoArgs,
}

func init() {
	config.RegisterFlags(rootCmd)
}

func Execute(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}
