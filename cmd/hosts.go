package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gausszhou/gossh/internal/host"
)

// `gossh hosts` manages the host inventory through the running server
// (GET/POST/PUT/DELETE /api/hosts) rather than by editing hosts.json.
//
// The file is not a safe interface for a live server: it is loaded once at
// startup and held in memory, so a write behind its back stays invisible and
// is clobbered by the server's next save. Going through the API also means
// the server reconciles the host's resident port forwards on every change
// (ADR-0007).

func buildHostsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "Manage the host inventory",
		Long: "Manage the host inventory served by a running gossh server.\n\n" +
			"The address and access token come from the same config file `gossh serve`\n" +
			"reads (default ~/.gossh/config.json), so a stock setup needs no flags.",
	}
	addAPIFlags(cmd)

	cmd.AddCommand(
		buildHostsLsCmd(),
		buildHostsAddCmd(),
		buildHostsShowCmd(),
		buildHostsEditCmd(),
		buildHostsRemoveCmd(),
		buildHostForwardsCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------------
// payload + flags
// ---------------------------------------------------------------------------

// hostPayload mirrors the server's hostRequest: a host record plus an
// optional one-shot password that is written to the keyring (never to
// hosts.json).
type hostPayload struct {
	host.Host
	Password     *string `json:"password,omitempty"`
	SavePassword bool    `json:"save_password,omitempty"`
}

// addCredentialFlags installs the three credential selectors. They are
// mutually exclusive.
func addCredentialFlags(cmd *cobra.Command) {
	cmd.Flags().String("key", "", "Private key file path (credential kind \"key\")")
	cmd.Flags().Bool("agent", false, "Authenticate through ssh-agent (credential kind \"agent\")")
	cmd.Flags().Bool("password", false, "Authenticate with a password (credential kind \"password\")")
}

// credentialFromFlags reads the credential selectors. found reports whether
// the caller asked for a credential at all, so `edit` can leave an existing
// one untouched.
func credentialFromFlags(cmd *cobra.Command) (cred host.Credential, found bool, err error) {
	keyPath, _ := cmd.Flags().GetString("key")
	useAgent, _ := cmd.Flags().GetBool("agent")
	usePassword, _ := cmd.Flags().GetBool("password")

	switch {
	case cmd.Flags().Changed("key") && keyPath == "":
		return cred, false, errors.New("--key needs a path")
	case cmd.Flags().Changed("key") && (useAgent || usePassword):
		return cred, false, errors.New("--key, --agent and --password are mutually exclusive")
	case useAgent && usePassword:
		return cred, false, errors.New("--key, --agent and --password are mutually exclusive")
	case cmd.Flags().Changed("key"):
		return host.Credential{Kind: host.CredKey, KeyPath: keyPath}, true, nil
	case useAgent:
		return host.Credential{Kind: host.CredAgent}, true, nil
	case usePassword:
		return host.Credential{Kind: host.CredPassword}, true, nil
	}
	return cred, false, nil
}

// addSecretFlag installs --secret. Reading the value is deferred to
// secretFromFlags so "-" can pull it from stdin instead of the argv (which is
// world-readable through the process list).
func addSecretFlag(cmd *cobra.Command) {
	cmd.Flags().String("secret", "", "Secret to store in the system keyring (use \"-\" to read it from stdin)")
}

func secretFromFlags(cmd *cobra.Command) (string, bool, error) {
	secret, _ := cmd.Flags().GetString("secret")
	switch secret {
	case "":
		return "", false, nil
	case "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false, fmt.Errorf("reading the secret from stdin: %w", err)
		}
		value := strings.TrimRight(string(data), "\r\n")
		if value == "" {
			return "", false, errors.New("stdin carried no secret")
		}
		return value, true, nil
	default:
		return secret, true, nil
	}
}

// saveSecret stores a secret in the system keyring: a password rides along
// with the host write, a key passphrase needs its own call because the host
// endpoints only handle passwords.
func (c *apiClient) saveSecret(h host.Host, secret string) error {
	switch h.Credential.Kind {
	case host.CredPassword:
		return nil // handled by the host request's save_password
	case host.CredKey:
		if h.Credential.KeyPath == "" {
			return errors.New("cannot store a passphrase without a key path")
		}
		body := map[string]string{
			"kind":     "passphrase",
			"key_path": h.Credential.KeyPath,
			"secret":   secret,
		}
		var res map[string]bool
		return c.call(http.MethodPost, "/api/secrets", body, &res)
	default:
		return fmt.Errorf("a secret can only be stored for a \"password\" or \"key\" credential, not %q", h.Credential.Kind)
	}
}

// ---------------------------------------------------------------------------
// output
// ---------------------------------------------------------------------------

func credentialLabel(h host.Host) string {
	switch h.Credential.Kind {
	case host.CredKey:
		if h.Credential.KeyPath != "" {
			return "key (" + h.Credential.KeyPath + ")"
		}
		return "key"
	case "":
		return string(host.CredDefault)
	default:
		return string(h.Credential.Kind)
	}
}

func formatHosts(list []host.Host) string {
	rows := make([][]string, 0, len(list)+1)
	rows = append(rows, []string{"ID", "NAME", "USER@ADDRESS", "CREDENTIAL"})
	for _, h := range list {
		name := h.Name
		if h.Builtin {
			name += " (builtin)"
		}
		addr := h.Addr()
		if h.Builtin {
			addr = h.Address
		}
		rows = append(rows, []string{h.ID, name, h.User + "@" + addr, credentialLabel(h)})
	}
	return renderTable(rows)
}

func describeHost(h host.Host) string {
	var b strings.Builder
	writeField := func(key, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%-11s %s\n", key+":", value)
		}
	}
	writeField("id", h.ID)
	writeField("name", h.Name)
	writeField("address", h.Addr())
	writeField("user", h.User)
	writeField("credential", credentialLabel(h))
	if len(h.Forwards) > 0 {
		fmt.Fprintf(&b, "%-11s %d (see `gossh hosts forwards ls %s`)\n", "forwards:", len(h.Forwards), h.ID)
	}
	if h.Builtin {
		writeField("builtin", "true (the local server: connect-only)")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------------------------------------------------------------------------
// subcommands
// ---------------------------------------------------------------------------

func buildHostsLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the hosts in the inventory",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			list, err := c.listHosts()
			if err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(list)
			}
			if len(list) == 0 {
				fmt.Fprintln(os.Stdout, "(no hosts; add one with `gossh hosts add`)")
				return nil
			}
			fmt.Fprintln(os.Stdout, formatHosts(list))
			return nil
		},
	}
}

func buildHostsAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add --name NAME --address ADDR --user USER [flags]",
		Short: "Add a host to the inventory",
		Long: "Add a host. The record is written by the server, so the running\n" +
			"instance picks it up immediately.\n\n" +
			"--secret stores a credential in the system keyring: the password when\n" +
			"the credential is \"password\", the passphrase when it is a key. Pass\n" +
			"\"-\" to read it from stdin instead of the command line.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			address, _ := cmd.Flags().GetString("address")
			user, _ := cmd.Flags().GetString("user")
			if name == "" || address == "" || user == "" {
				return errors.New("--name, --address and --user are required")
			}
			port, _ := cmd.Flags().GetInt("port")
			cred, found, err := credentialFromFlags(cmd)
			if err != nil {
				return err
			}
			if !found {
				cred = host.Credential{Kind: host.CredDefault}
			}
			secret, hasSecret, err := secretFromFlags(cmd)
			if err != nil {
				return err
			}

			payload := hostPayload{Host: host.Host{
				Name: name, Address: address, Port: port, User: user, Credential: cred,
			}}
			if hasSecret && cred.Kind == host.CredPassword {
				payload.Password = &secret
				payload.SavePassword = true
			}

			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			var created host.Host
			if err := c.call(http.MethodPost, "/api/hosts", payload, &created); err != nil {
				return err
			}
			if hasSecret && cred.Kind == host.CredKey {
				if err := c.saveSecret(created, secret); err != nil {
					return err
				}
			}
			if cliJSON(cmd) {
				return printJSON(created)
			}
			fmt.Fprintf(os.Stdout, "Added host %s (id: %s)\n", created.Name, created.ID)
			return nil
		},
	}
	cmd.Flags().String("name", "", "Host name (unique)")
	cmd.Flags().String("address", "", "Host address (IP or hostname)")
	cmd.Flags().String("user", "", "Remote user")
	cmd.Flags().Int("port", 0, "SSH port (default 22)")
	addCredentialFlags(cmd)
	addSecretFlag(cmd)
	return cmd
}

func buildHostsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id|name>",
		Short: "Show one host record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			h, err := c.getHost(args[0])
			if err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(h)
			}
			fmt.Fprintln(os.Stdout, describeHost(h))
			return nil
		},
	}
}

func buildHostsEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit <id|name> [flags]",
		Short: "Edit a host record",
		Long: "Edit a host. The record is read, the flags you pass are applied, and\n" +
			"the result is written back — anything you do not mention is kept.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cred, hasCred, err := credentialFromFlags(cmd)
			if err != nil {
				return err
			}
			secret, hasSecret, err := secretFromFlags(cmd)
			if err != nil {
				return err
			}

			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			h, err := c.getHost(args[0])
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("name") {
				h.Name, _ = cmd.Flags().GetString("name")
			}
			if cmd.Flags().Changed("address") {
				h.Address, _ = cmd.Flags().GetString("address")
			}
			if cmd.Flags().Changed("user") {
				h.User, _ = cmd.Flags().GetString("user")
			}
			if cmd.Flags().Changed("port") {
				h.Port, _ = cmd.Flags().GetInt("port")
			}
			if hasCred {
				h.Credential = cred
			}

			payload := hostPayload{Host: h}
			if hasSecret && h.Credential.Kind == host.CredPassword {
				payload.Password = &secret
				payload.SavePassword = true
			}

			var updated host.Host
			if err := c.call(http.MethodPut, "/api/hosts/"+h.ID, payload, &updated); err != nil {
				return err
			}
			if hasSecret && updated.Credential.Kind == host.CredKey {
				if err := c.saveSecret(updated, secret); err != nil {
					return err
				}
			}
			if cliJSON(cmd) {
				return printJSON(updated)
			}
			fmt.Fprintf(os.Stdout, "Updated host %s\n", updated.ID)
			return nil
		},
	}
	cmd.Flags().String("name", "", "New host name")
	cmd.Flags().String("address", "", "New address")
	cmd.Flags().String("user", "", "New remote user")
	cmd.Flags().Int("port", 0, "New SSH port")
	addCredentialFlags(cmd)
	addSecretFlag(cmd)
	return cmd
}

func buildHostsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <id|name>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a host (matched by id, then by name)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			id, err := c.resolveHost(args[0])
			if err != nil {
				return err
			}
			if err := c.call(http.MethodDelete, "/api/hosts/"+id, nil, nil); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(map[string]any{"id": id, "removed": true})
			}
			fmt.Fprintf(os.Stdout, "Removed host %s\n", id)
			return nil
		},
	}
}
