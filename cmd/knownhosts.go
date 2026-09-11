package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// `gossh known-hosts` manages the TOFU pins (CONTEXT.md → 主机密钥指纹) held
// by the server, which is the process that actually owns the trust store.
// Forgetting a pin makes the next connection to that address trust the key
// it finds again.

// knownHostPin mirrors the handler's pinView in internal/api/misc_handler.go.
type knownHostPin struct {
	Addr        string `json:"addr"`
	KeyType     string `json:"key_type"`
	Fingerprint string `json:"fingerprint"`
	FirstSeen   int64  `json:"first_seen"`
}

func buildKnownHostsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "known-hosts",
		Aliases: []string{"knownhosts"},
		Short:   "Inspect and forget TOFU host-key pins",
		Long: "The server keeps its own trust store (`~/.gossh/known_hosts`), separate\n" +
			"from OpenSSH's. Forgetting an entry means the next connection to that\n" +
			"address is trusted on first use again — the fix for a legitimately\n" +
			"changed host key.",
	}
	addAPIFlags(cmd)
	cmd.AddCommand(buildKnownHostsLsCmd(), buildKnownHostsForgetCmd())
	return cmd
}

func buildKnownHostsLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the pinned host keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			var pins []knownHostPin
			if err := c.call(http.MethodGet, "/api/known-hosts", nil, &pins); err != nil {
				return err
			}
			if pins == nil {
				pins = []knownHostPin{}
			}
			if cliJSON(cmd) {
				return printJSON(pins)
			}
			if len(pins) == 0 {
				fmt.Fprintln(os.Stdout, "(no pinned host keys)")
				return nil
			}
			rows := make([][]string, 0, len(pins)+1)
			rows = append(rows, []string{"ADDRESS", "KEY TYPE", "FINGERPRINT", "FIRST SEEN"})
			for _, p := range pins {
				seen := "-"
				if p.FirstSeen > 0 {
					seen = time.Unix(p.FirstSeen, 0).Format(time.RFC3339)
				}
				rows = append(rows, []string{p.Addr, p.KeyType, p.Fingerprint, seen})
			}
			fmt.Fprintln(os.Stdout, renderTable(rows))
			return nil
		},
	}
}

func buildKnownHostsForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "forget <address>",
		Aliases: []string{"rm", "delete"},
		Short:   "Forget a pinned host key (re-trust on first use)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr := args[0]
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			if err := c.call(http.MethodDelete, "/api/known-hosts/"+url.PathEscape(addr), nil, nil); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(map[string]any{"addr": addr, "forgotten": true})
			}
			fmt.Fprintf(os.Stdout, "Forgot host key for %s\n", addr)
			return nil
		},
	}
}
