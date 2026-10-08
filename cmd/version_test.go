package cmd

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/krau/SaveAny-Bot/config"
)

func TestUpgradeRejectsInvalidReleaseVersions(t *testing.T) {
	previous := config.Version
	t.Cleanup(func() { config.Version = previous })
	for _, version := range []string{"dev", "", "v1.2.3", "invalid"} {
		t.Run(version, func(t *testing.T) {
			config.Version = version
			// Rejection must happen before network access or binary replacement.
			if err := upgradeCmd.RunE(&cobra.Command{}, nil); err == nil {
				t.Fatal("invalid release version returned success")
			}
		})
	}
}
