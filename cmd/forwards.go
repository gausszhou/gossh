package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/gausszhou/gossh/internal/host"
)

// Port forwards exist at two levels (CONTEXT.md → 端口转发):
//
//   - host-level, stored on the host record and applied by the host's own
//     resident forward connection (ADR-0007), independent of any session —
//     `gossh hosts forwards`;
//   - session-level, added at runtime and valid only for that session —
//     `gossh session forwards`.
//
// Both speak the same three kinds: local (-L), remote (-R), dynamic (-D).

var forwardKinds = []string{"local", "remote", "dynamic"}

// addForwardFlags installs the shared --kind/--bind/--target triple.
func addForwardFlags(cmd *cobra.Command) {
	cmd.Flags().String("kind", "local", "Forward kind: local (-L), remote (-R) or dynamic (-D, SOCKS5)")
	cmd.Flags().String("bind", "", "Listen address, e.g. 127.0.0.1:8080")
	cmd.Flags().String("target", "", "Target address, e.g. localhost:80 (omit for dynamic)")
}

// readForwardFlags validates the flags the same way the server does, before
// any request goes out.
func readForwardFlags(cmd *cobra.Command) (host.Forward, error) {
	kind, _ := cmd.Flags().GetString("kind")
	bind, _ := cmd.Flags().GetString("bind")
	target, _ := cmd.Flags().GetString("target")

	if bind == "" {
		return host.Forward{}, errors.New("--bind is required")
	}
	switch kind {
	case "local", "remote":
		if target == "" {
			return host.Forward{}, fmt.Errorf("--target is required for a %s forward", kind)
		}
	case "dynamic":
		target = ""
	default:
		return host.Forward{}, fmt.Errorf("--kind must be one of %v", forwardKinds)
	}
	return host.Forward{Kind: kind, Bind: bind, Target: target}, nil
}

// formatForwards renders one table for either level. status is empty for
// session-level forwards (they are alive for as long as they are listed).
func formatForwards(rows []forwardRow) string {
	header := []string{"ID", "KIND", "BIND", "TARGET"}
	for _, r := range rows {
		if r.Status != "" {
			header = append(header, "STATUS")
			break
		}
	}
	table := make([][]string, 0, len(rows)+1)
	table = append(table, header)
	for _, r := range rows {
		row := []string{r.ID, r.Kind, r.Bind, r.Target}
		if len(header) == 5 {
			status := r.Status
			if r.Error != "" {
				status += ": " + r.Error
			}
			row = append(row, status)
		}
		table = append(table, row)
	}
	return renderTable(table)
}

type forwardRow struct {
	ID     string
	Kind   string
	Bind   string
	Target string
	Status string
	Error  string
}

// printForwards emits the rows in the mode cliJSON picks. payload is whatever
// the command wants to hand to a machine-readable consumer.
func printForwards(cmd *cobra.Command, rows []forwardRow, payload any) error {
	if cliJSON(cmd) {
		return printJSON(payload)
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stdout, "(no forwards)")
		return nil
	}
	fmt.Fprintln(os.Stdout, formatForwards(rows))
	return nil
}

// ---------------------------------------------------------------------------
// session-level forwards
// ---------------------------------------------------------------------------

func buildSessionForwardsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forwards",
		Short: "Manage a session's temporary port forwards",
		Long: "Port forwards that live on the session's own SSH connection.\n\n" +
			"They exist only while the session does, and they need an SSH\n" +
			"connection — a local-server session has none, so adding a forward to\n" +
			"it fails. For forwards that outlive sessions, configure them on the\n" +
			"host record instead: `gossh hosts forwards`.",
	}
	cmd.AddCommand(
		buildSessionForwardsLsCmd(),
		buildSessionForwardsAddCmd(),
		buildSessionForwardsRmCmd(),
	)
	return cmd
}

// sessionForwardEntry mirrors api.ForwardEntry.
type sessionForwardEntry struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Bind   string `json:"bind"`
	Target string `json:"target"`
}

func (c *apiClient) listSessionForwards(sessionID string) ([]sessionForwardEntry, error) {
	var entries []sessionForwardEntry
	if err := c.call(http.MethodGet, "/api/sessions/"+sessionID+"/forwards", nil, &entries); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []sessionForwardEntry{}
	}
	return entries, nil
}

func sessionForwardRows(entries []sessionForwardEntry) []forwardRow {
	rows := make([]forwardRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, forwardRow{ID: e.ID, Kind: e.Kind, Bind: e.Bind, Target: e.Target})
	}
	return rows
}

func buildSessionForwardsLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List a session's port forwards",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			sess, err := c.resolveSession(cmd)
			if err != nil {
				return err
			}
			entries, err := c.listSessionForwards(sess.ID)
			if err != nil {
				return err
			}
			return printForwards(cmd, sessionForwardRows(entries), map[string]any{
				"session_id": sess.ID,
				"forwards":   entries,
			})
		},
	}
}

func buildSessionForwardsAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add --kind <local|remote|dynamic> --bind <addr> [--target <addr>]",
		Short: "Add a forward to a session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := readForwardFlags(cmd)
			if err != nil {
				return err
			}
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			sess, err := c.resolveSession(cmd)
			if err != nil {
				return err
			}
			var entry sessionForwardEntry
			if err := c.call(http.MethodPost, "/api/sessions/"+sess.ID+"/forwards", spec, &entry); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(entry)
			}
			fmt.Fprintf(os.Stdout, "Added forward %s to %s\n", entry.ID, sess.ID)
			return nil
		},
	}
	addForwardFlags(cmd)
	return cmd
}

func buildSessionForwardsRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <forward-id>",
		Aliases: []string{"delete"},
		Short:   "Remove a session forward by its id",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			sess, err := c.resolveSession(cmd)
			if err != nil {
				return err
			}
			path := "/api/sessions/" + sess.ID + "/forwards/" + args[0]
			if err := c.call(http.MethodDelete, path, nil, nil); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(map[string]any{"session_id": sess.ID, "removed": args[0]})
			}
			fmt.Fprintf(os.Stdout, "Removed forward %s from %s\n", args[0], sess.ID)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// host-level forwards
// ---------------------------------------------------------------------------

func buildHostForwardsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forwards",
		Short: "Manage a host's resident port forwards",
		Long: "Port forwards stored on the host record and applied by the host's\n" +
			"own forward connection (ADR-0007). They do not depend on any session:\n" +
			"once the connection is up they stay until the config changes.\n\n" +
			"Adding or removing one rewrites the host record (PUT /api/hosts/{id}),\n" +
			"which makes the server reconcile its running forwards immediately.",
	}
	cmd.AddCommand(
		buildHostForwardsLsCmd(),
		buildHostForwardsAddCmd(),
		buildHostForwardsRmCmd(),
	)
	return cmd
}

// hostForwardEntry mirrors api.HostForward (config + runtime status).
type hostForwardEntry struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Bind   string `json:"bind"`
	Target string `json:"target"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func buildHostForwardsLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <host-id|name>",
		Short: "List a host's forwards and their runtime status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			id, err := c.resolveHost(args[0])
			if err != nil {
				return err
			}
			var entries []hostForwardEntry
			if err := c.call(http.MethodGet, "/api/hosts/"+id+"/forwards", nil, &entries); err != nil {
				return err
			}
			if entries == nil {
				entries = []hostForwardEntry{}
			}
			rows := make([]forwardRow, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, forwardRow{
					ID: e.ID, Kind: e.Kind, Bind: e.Bind, Target: e.Target,
					Status: e.Status, Error: e.Error,
				})
			}
			return printForwards(cmd, rows, map[string]any{"host_id": id, "forwards": entries})
		},
	}
}

func buildHostForwardsAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <host-id|name> --kind <local|remote|dynamic> --bind <addr> [--target <addr>]",
		Short: "Add a resident forward to a host record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := readForwardFlags(cmd)
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
			h.Forwards = append(h.Forwards, spec)
			if err := c.putHost(h); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(h)
			}
			fmt.Fprintf(os.Stdout, "Added %s forward to %s: %s -> %s\n",
				spec.Kind, h.Name, spec.Bind, describeForwardTarget(spec))
			return nil
		},
	}
	addForwardFlags(cmd)
	return cmd
}

func buildHostForwardsRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm <host-id|name> --bind <addr> [--kind <kind>]",
		Aliases: []string{"delete"},
		Short:   "Remove a resident forward from a host record",
		Long: "Remove the host forward whose bind matches --bind (and --kind when\n" +
			"given; two forwards may share a bind with different kinds).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bind, _ := cmd.Flags().GetString("bind")
			kind, _ := cmd.Flags().GetString("kind")
			if bind == "" {
				return errors.New("--bind is required")
			}
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			h, err := c.getHost(args[0])
			if err != nil {
				return err
			}
			kept := make([]host.Forward, 0, len(h.Forwards))
			removed := 0
			for _, f := range h.Forwards {
				if f.Bind == bind && (kind == "" || f.Kind == kind) {
					removed++
					continue
				}
				kept = append(kept, f)
			}
			if removed == 0 {
				return fmt.Errorf("no forward on %s binds %s", h.Name, bind)
			}
			h.Forwards = kept
			if err := c.putHost(h); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(h)
			}
			fmt.Fprintf(os.Stdout, "Removed %d forward(s) from %s\n", removed, h.Name)
			return nil
		},
	}
	cmd.Flags().String("bind", "", "Bind address of the forward to remove")
	cmd.Flags().String("kind", "", "Only remove forwards of this kind")
	return cmd
}

// putHost replaces the host record (PUT /api/hosts/{id}); the server keeps
// created_at and re-reconciles the resident forwards.
func (c *apiClient) putHost(h host.Host) error {
	return c.call(http.MethodPut, "/api/hosts/"+h.ID, h, nil)
}

func describeForwardTarget(f host.Forward) string {
	if f.Target == "" {
		return "(dynamic SOCKS5)"
	}
	return f.Target
}
