package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"
)

// `gossh secrets` writes credentials into the system keyring through the
// server (POST/DELETE /api/secrets). The keyring is the server process's, and
// only it knows whether one is available at all.

func buildSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Store or delete credentials in the system keyring",
		Long: "Manage the keyring entries the server uses to connect: SSH passwords\n" +
			"(keyed by addr+user) and private-key passphrases (keyed by key path).\n\n" +
			"A password supplied with `hosts add --secret` or `session create\n" +
			"--password --save` lands in the same store.",
	}
	addAPIFlags(cmd)
	cmd.AddCommand(buildSecretsSetCmd(), buildSecretsRmCmd())
	return cmd
}

// addSecretSelectorFlags installs the flags that say *which* secret is meant.
func addSecretSelectorFlags(cmd *cobra.Command) {
	cmd.Flags().String("kind", "", "Kind of secret: \"password\" or \"passphrase\"")
	cmd.Flags().String("addr", "", "Host address, e.g. 10.0.0.5:22 (for \"password\")")
	cmd.Flags().String("user", "", "Remote user (for \"password\")")
	cmd.Flags().String("key", "", "Private key path (for \"passphrase\")")
}

// readSecretSelector validates the selector flags the way the API does.
func readSecretSelector(cmd *cobra.Command) (kind, addr, user, keyPath string, err error) {
	kind, _ = cmd.Flags().GetString("kind")
	addr, _ = cmd.Flags().GetString("addr")
	user, _ = cmd.Flags().GetString("user")
	keyPath, _ = cmd.Flags().GetString("key")

	switch kind {
	case "password":
		if addr == "" || user == "" {
			return "", "", "", "", errors.New("--addr and --user are required for a password")
		}
	case "passphrase":
		if keyPath == "" {
			return "", "", "", "", errors.New("--key is required for a passphrase")
		}
	default:
		return "", "", "", "", errors.New("--kind must be \"password\" or \"passphrase\"")
	}
	return kind, addr, user, keyPath, nil
}

func buildSecretsSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set --kind password --addr A --user U [--secret S]",
		Short: "Store a credential in the system keyring",
		Long: "Store a secret in the system keyring.\n\n" +
			"Pass `--secret -` to read the value from stdin, which keeps it out of\n" +
			"the process list. A bare --secret VALUE is visible to anyone who can\n" +
			"list processes on this machine.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind, addr, user, keyPath, err := readSecretSelector(cmd)
			if err != nil {
				return err
			}
			secret, hasSecret, err := secretFromFlags(cmd)
			if err != nil {
				return err
			}
			if !hasSecret {
				return errors.New("--secret is required (use \"--secret -\" to read it from stdin)")
			}

			body := map[string]string{"kind": kind, "secret": secret}
			if kind == "password" {
				body["addr"], body["user"] = addr, user
			} else {
				body["key_path"] = keyPath
			}

			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			if err := c.call(http.MethodPost, "/api/secrets", body, nil); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(map[string]any{"kind": kind, "saved": true})
			}
			fmt.Fprintf(os.Stdout, "Stored %s in the system keyring\n", describeSecret(kind, addr, user, keyPath))
			return nil
		},
	}
	addSecretSelectorFlags(cmd)
	addSecretFlag(cmd)
	return cmd
}

func buildSecretsRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm --kind password --addr A --user U",
		Aliases: []string{"delete"},
		Short:   "Delete a credential from the system keyring",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind, addr, user, keyPath, err := readSecretSelector(cmd)
			if err != nil {
				return err
			}
			query := url.Values{"kind": {kind}}
			if kind == "password" {
				query.Set("addr", addr)
				query.Set("user", user)
			} else {
				query.Set("key_path", keyPath)
			}

			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			if err := c.call(http.MethodDelete, "/api/secrets?"+query.Encode(), nil, nil); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(map[string]any{"kind": kind, "removed": true})
			}
			fmt.Fprintf(os.Stdout, "Deleted %s from the system keyring\n", describeSecret(kind, addr, user, keyPath))
			return nil
		},
	}
	addSecretSelectorFlags(cmd)
	return cmd
}

func describeSecret(kind, addr, user, keyPath string) string {
	if kind == "passphrase" {
		return "passphrase for " + keyPath
	}
	return fmt.Sprintf("password for %s@%s", user, addr)
}
