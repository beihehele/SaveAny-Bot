package config

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestConfigFlagInheritedBySubcommand(t *testing.T) {
	t.Cleanup(viper.Reset)
	for _, args := range [][]string{{"--config", "custom.toml", "child"}, {"child", "--config", "custom.toml"}} {
		root := &cobra.Command{Use: "test"}
		RegisterFlags(root)
		got := ""
		root.AddCommand(&cobra.Command{Use: "child", Run: func(cmd *cobra.Command, _ []string) { got = GetConfigFile(cmd) }})
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if got != "custom.toml" {
			t.Fatalf("config=%q args=%v", got, args)
		}
	}
}
