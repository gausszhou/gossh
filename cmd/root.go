package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// Version and CommitID are injected at build time via -ldflags.
var (
	Version  = "unknown_version"
	CommitID = "unknown_commit"
)

var rootCmd = &cobra.Command{
	Use:           "gossh",
	Short:         "SSH client with a browser UI",
	Version:       Version + "+" + CommitID,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// --config is a persistent flag so it works in both
	// `gossh --config x serve` and `gossh serve --config x`.
	configPath := os.Getenv("GOSSH_CONFIG")
	if configPath == "" {
		configPath = "~/.gossh/config.json"
	}
	rootCmd.PersistentFlags().String("config", configPath, "Config file path (GOSSH_CONFIG env var)")

	rootCmd.AddCommand(buildServeCmd())
	rootCmd.AddCommand(buildAppCmd())
	rootCmd.AddCommand(buildHostsCmd())
	registerRunCmd(rootCmd) // `gossh run` 按编译开关注册(Makefile RUN=1)
	rootCmd.AddCommand(buildVersionCmd())
}
