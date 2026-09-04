package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/utils"
)

// defaultHostsFile is where `gossh hosts` looks unless --hosts-file
// (or the GOSSH_HOSTS_FILE env) says otherwise. Must match the serve
// default so CLI and UI share one inventory.
func defaultHostsFile() string {
	if p := os.Getenv("GOSSH_HOSTS_FILE"); p != "" {
		return p
	}
	return "~/.gossh/hosts.json"
}

func buildHostsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "Manage the host inventory",
		Long: "Manage the host inventory stored at ~/.gossh/hosts.json\n" +
			"(the same file the server serves; CLI and UI share it).",
	}
	cmd.PersistentFlags().String("hosts-file", defaultHostsFile(), "Host inventory file path")

	cmd.AddCommand(buildHostsAddCmd())
	cmd.AddCommand(buildHostsListCmd())
	cmd.AddCommand(buildHostsRemoveCmd())
	return cmd
}

func loadInventoryForCmd(cmd *cobra.Command) (*host.Inventory, error) {
	path, _ := cmd.Flags().GetString("hosts-file")
	return host.LoadInventory(utils.Expand(path))
}

func buildHostsAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add --name NAME --address ADDR --user USER [flags]",
		Short: "Add a host to the inventory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			address, _ := cmd.Flags().GetString("address")
			user, _ := cmd.Flags().GetString("user")
			port, _ := cmd.Flags().GetInt("port")
			group, _ := cmd.Flags().GetString("group")
			via, _ := cmd.Flags().GetString("via")

			credKind := host.CredDefault
			keyPath, _ := cmd.Flags().GetString("key")
			useAgent, _ := cmd.Flags().GetBool("agent")
			usePassword, _ := cmd.Flags().GetBool("password")
			switch {
			case keyPath != "":
				credKind = host.CredKey
			case useAgent:
				credKind = host.CredAgent
			case usePassword:
				credKind = host.CredPassword
			}

			inv, err := loadInventoryForCmd(cmd)
			if err != nil {
				return err
			}
			h := &host.Host{
				ID:      host.NewID(),
				Name:    name,
				Address: address,
				Port:    port,
				User:    user,
				Group:   group,
				Via:     via,
				Credential: host.Credential{
					Kind:    credKind,
					KeyPath: keyPath,
				},
			}
			if err := inv.Add(h); err != nil {
				return err
			}
			fmt.Printf("Added host %s (id: %s)\n", h.Name, h.ID)
			return nil
		},
	}
	cmd.Flags().String("name", "", "Host name (unique)")
	cmd.Flags().String("address", "", "Host address (IP or hostname)")
	cmd.Flags().String("user", "", "Remote user")
	cmd.Flags().Int("port", 0, "SSH port (default 22)")
	cmd.Flags().String("group", "", "Inventory group")
	cmd.Flags().String("via", "", "Jump host id (ProxyJump chain)")
	cmd.Flags().String("key", "", "Private key file path (credential kind = key)")
	cmd.Flags().Bool("agent", false, "Authenticate via ssh-agent only")
	cmd.Flags().Bool("password", false, "Authenticate with password (keyring)")
	return cmd
}

func buildHostsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List hosts in the inventory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			inv, err := loadInventoryForCmd(cmd)
			if err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			hosts := inv.List()
			if asJSON {
				b, _ := json.MarshalIndent(hosts, "", "  ")
				fmt.Println(string(b))
				return nil
			}
			for _, h := range hosts {
				via := ""
				if h.Via != "" {
					via = " via " + h.Via
				}
				fmt.Printf("%-24s %-10s %s@%s%s\n", h.ID, "["+h.Group+"]", h.User, h.Addr(), via)
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func buildHostsRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <id|name>",
		Short: "Remove a host (matched by id, then by name)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			inv, err := loadInventoryForCmd(cmd)
			if err != nil {
				return err
			}
			query := args[0]
			id := ""
			if _, err := inv.Get(query); err == nil {
				id = query
			} else {
				for _, h := range inv.List() {
					if h.Name == query {
						id = h.ID
						break
					}
				}
			}
			if id == "" {
				return fmt.Errorf("no host matches %q", query)
			}
			if err := inv.Remove(id); err != nil {
				return err
			}
			fmt.Printf("Removed host %s\n", id)
			return nil
		},
	}
	return cmd
}
