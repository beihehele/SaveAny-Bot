package cmd

import "testing"

func TestCurrentCLICommandSurface(t *testing.T) {
	names := map[string]bool{"version": true, "admin-password": true}
	if len(rootCmd.Commands()) != len(names) {
		t.Fatal("unexpected CLI command count")
	}
	for _, command := range rootCmd.Commands() {
		if !names[command.Name()] {
			t.Fatalf("undefined CLI command: %s", command.Name())
		}
	}
	for name := range names {
		command, _, err := rootCmd.Find([]string{name})
		if err != nil || command == rootCmd || command.Name() != name {
			t.Fatalf("current CLI command unavailable: %s", name)
		}
	}
	if rootCmd.Args == nil || rootCmd.Args(rootCmd, []string{"unknown"}) == nil {
		t.Fatal("undefined command can fall through to Bot startup")
	}
}
