package cmd

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// `gossh title` reads and writes the deployment-wide page title (GET/PUT
// /api/title), which the web UI applies as the browser tab title. It is
// persisted by the server and survives restarts.

func buildTitleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "title",
		Short: "Read or set the deployment-wide page title",
		Long: "The page title the browser UI puts on the tab. Stored by the server\n" +
			"(default: `~/.gossh/title`), so it survives a restart and applies to\n" +
			"every browser that opens the UI.",
	}
	addAPIFlags(cmd)
	cmd.AddCommand(buildTitleGetCmd(), buildTitleSetCmd(), buildTitleClearCmd())
	return cmd
}

func buildTitleGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Print the current page title",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(cmd)
			if err != nil {
				return err
			}
			var res struct {
				Title string `json:"title"`
			}
			if err := c.call(http.MethodGet, "/api/title", nil, &res); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(res)
			}
			if res.Title == "" {
				fmt.Fprintln(os.Stdout, "(unset: the UI uses its built-in title)")
				return nil
			}
			fmt.Fprintln(os.Stdout, res.Title)
			return nil
		},
	}
}

func buildTitleSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <title>",
		Short: "Set the page title",
		Long: "Set the page title. The server trims it and caps it at 200 characters.\n" +
			"Use `gossh title clear` to go back to the built-in title.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return putTitle(cmd, strings.Join(args, " "))
		},
	}
}

func buildTitleClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Clear the page title (back to the built-in one)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return putTitle(cmd, "")
		},
	}
}

func putTitle(cmd *cobra.Command, title string) error {
	c, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	var res struct {
		Title string `json:"title"`
	}
	if err := c.call(http.MethodPut, "/api/title", map[string]string{"title": title}, &res); err != nil {
		return err
	}
	if cliJSON(cmd) {
		return printJSON(res)
	}
	if res.Title == "" {
		fmt.Fprintln(os.Stdout, "Cleared the page title")
		return nil
	}
	fmt.Fprintf(os.Stdout, "Page title set to %q\n", res.Title)
	return nil
}
