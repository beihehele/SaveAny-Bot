package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/krau/SaveAny-Bot/pkg/adminauth"
)

func init() {
	rootCmd.AddCommand(newAdminPasswordCommand())
}

func newAdminPasswordCommand() *cobra.Command {
	var stdin bool
	command := &cobra.Command{
		Use: "admin-password", Short: "Generate an admin password hash without starting the bot",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var password []byte
			defer func() { clear(password) }()
			var err error
			if stdin {
				password, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1027))
				password = bytes.TrimSuffix(bytes.TrimSuffix(password, []byte("\n")), []byte("\r"))
			} else {
				fd := int(os.Stdin.Fd())
				if !term.IsTerminal(fd) {
					return fmt.Errorf("use an interactive terminal or --stdin")
				}
				if _, err := fmt.Fprint(cmd.ErrOrStderr(), "Admin password (12+ bytes): "); err != nil {
					return err
				}
				password, err = term.ReadPassword(fd)
				if _, writeErr := fmt.Fprintln(cmd.ErrOrStderr()); writeErr != nil {
					return writeErr
				}
				if err == nil {
					if _, err := fmt.Fprint(cmd.ErrOrStderr(), "Confirm password: "); err != nil {
						return err
					}
					confirmation, confirmErr := term.ReadPassword(fd)
					defer clear(confirmation)
					if _, writeErr := fmt.Fprintln(cmd.ErrOrStderr()); writeErr != nil {
						return writeErr
					}
					matches := bytes.Equal(password, confirmation)
					clear(confirmation)
					if confirmErr != nil {
						return confirmErr
					}
					if !matches {
						clear(password)
						return fmt.Errorf("passwords do not match")
					}
				}
			}
			if err != nil {
				return err
			}
			hash, err := adminauth.Hash(password)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "password_hash = %q\n", hash)
			return err
		},
	}
	command.Flags().BoolVar(&stdin, "stdin", false, "Read password from stdin for secret-file based setup")
	return command
}
