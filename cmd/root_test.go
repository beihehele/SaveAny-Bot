package cmd

import "testing"

func TestSupportedCLIRejectsRemovedCommandsBeforeStartup(t *testing.T) {
	for _, name := range []string{"upload", "watch", "upgrade", "up"} {
		for _, command := range rootCmd.Commands() {
			if command.Name() == name {
				t.Fatalf("removed command remains: %s", name)
			}
		}
		if rootCmd.Args == nil || rootCmd.Args(rootCmd, []string{name}) == nil {
			t.Fatalf("%s can fall through to Bot startup", name)
		}
	}
	for _, name := range []string{"version", "admin-password"} {
		command, _, err := rootCmd.Find([]string{name})
		if err != nil || command == rootCmd || command.Name() != name {
			t.Fatalf("retained CLI command unavailable: %s", name)
		}
	}
	for _, flag := range []string{"parser-plugin-enable", "parser-plugin-dirs", "parser-proxy", "no-clean-cache"} {
		if rootCmd.PersistentFlags().Lookup(flag) != nil {
			t.Fatalf("obsolete flag remains: %s", flag)
		}
	}
}
