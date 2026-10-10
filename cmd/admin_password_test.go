package cmd

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/adminauth"
)

func TestAdminPasswordStdin(t *testing.T) {
	for _, ending := range []string{"", "\n", "\r\n"} {
		t.Run(strconv.Quote(ending), func(t *testing.T) {
			command := newAdminPasswordCommand()
			const password = "isolated-cli-password"
			command.SetIn(strings.NewReader(password + ending))
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetArgs([]string{"--stdin"})
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			hash, err := strconv.Unquote(strings.TrimSpace(strings.TrimPrefix(output.String(), "password_hash = ")))
			if err != nil {
				t.Fatal(err)
			}
			verifier, err := adminauth.Parse(hash)
			if err != nil || !verifier.Verify([]byte(password)) {
				t.Fatalf("generated hash does not verify: %v", err)
			}
			if strings.Contains(output.String(), password) {
				t.Fatal("plaintext password leaked")
			}
		})
	}
}

func TestAdminPasswordRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"short", strings.Repeat("x", 1025), strings.Repeat("x", 1027) + "\n"} {
		command := newAdminPasswordCommand()
		command.SetIn(strings.NewReader(input))
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetArgs([]string{"--stdin"})
		if err := command.Execute(); err == nil {
			t.Fatal("invalid password accepted")
		}
		if strings.Contains(output.String(), "password_hash =") || strings.Contains(output.String(), input) {
			t.Fatal("invalid input produced a hash or leaked plaintext")
		}
	}
}
