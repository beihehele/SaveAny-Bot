package cmd

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/blang/semver"
	"github.com/spf13/cobra"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/updater"
)

var VersionCmd = &cobra.Command{
	Use:     "version",
	Aliases: []string{"v"},
	Short:   "Print the version number of saveany-bot",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("saveany-bot version: %s %s/%s\nBuildTime: %s, Commit: %s, Go: %s\n", config.Version, runtime.GOOS, runtime.GOARCH, config.BuildTime, config.GitCommit, runtime.Version())
	},
}

var upgradeCmd = &cobra.Command{
	Use:     "upgrade",
	Aliases: []string{"up"},
	Short:   "Upgrade saveany-bot to the latest version",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := semver.Parse(config.Version)
		if err != nil {
			return fmt.Errorf("cannot upgrade: invalid release version: %w", err)
		}
		if err := updater.CheckEnvironment(config.Docker == "true"); err != nil {
			return err
		}
		latest, found, err := updater.DetectLatest(cmd.Context(), config.GitRepo)
		if err != nil {
			return fmt.Errorf("detect latest release: %w", err)
		}
		if !found {
			cmd.Println("No releases found")
			return nil
		}
		if err := updater.CheckUpgrade(v, latest); err != nil {
			if errors.Is(err, updater.ErrAlreadyLatest) {
				cmd.Println("Current binary is the latest version", config.Version)
				return nil
			}
			return fmt.Errorf("cannot upgrade %s to %s: %w", v, latest.Version(), err)
		}
		if err := latest.VerificationError(); err != nil {
			return err
		}
		cmd.Printf("Updating to version %s...\n", latest.Version())
		backup, err := updater.Apply(cmd.Context(), v, latest)
		if err != nil {
			return fmt.Errorf("update failed: %w", err)
		}
		cmd.Println("Successfully updated to version", latest.Version())
		cmd.Println("Previous binary saved at", backup)
		cmd.Println("Release note:\n", latest.ReleaseNotes())
		return nil
	},
}

func init() {
	rootCmd.AddCommand(VersionCmd)
	rootCmd.AddCommand(upgradeCmd)
}
