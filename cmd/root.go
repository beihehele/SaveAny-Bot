package cmd

import (
	"context"

	"github.com/krau/SaveAny-Bot/cmd/upload"
	"github.com/krau/SaveAny-Bot/cmd/watch"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "saveany-bot",
	Short: "saveany-bot",
	Run:   Run,
}

func init() {
	config.RegisterFlags(rootCmd)
	upload.Register(rootCmd)
	watch.Register(rootCmd)
}

func Execute(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}
