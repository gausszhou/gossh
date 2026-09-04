package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/gausszhou/gossh/internal/api"
	"github.com/gausszhou/gossh/internal/config"
	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/session"
	"github.com/gausszhou/gossh/internal/sshx"
	"github.com/gausszhou/gossh/internal/terminal"
	"github.com/gausszhou/gossh/internal/utils"
)

// setupLogFile writes the server log to path (append mode) in addition
// to the console. Empty path keeps the console-only behavior.
func setupLogFile(path string) error {
	if path == "" {
		return nil
	}

	logPath := utils.Expand(path)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("failed to create log directory `%s`: %w", filepath.Dir(logPath), err)
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open log file `%s`: %w", logPath, err)
	}

	log.SetOutput(io.MultiWriter(os.Stderr, file))
	log.Printf("Server log file: %s", logPath)
	return nil
}

var (
	appOptions      = &api.Options{}
	terminalOptions = &terminal.Options{}
	mappings        map[string]string
)

// buildServeCmd creates the `gossh serve` subcommand.
//
// Usage: gossh serve [flags]
//
// The server owns the host inventory, the TOFU host-key store and the
// keyring-backed secrets, and serves the browser UI on 127.0.0.1 with a
// printed access-token URL (see CONTEXT.md → 访问令牌).
func buildServeCmd() *cobra.Command {
	serveCmd := &cobra.Command{
		Use:   "serve [flags]",
		Short: "Start the SSH web client server",
		Long: "Start the SSH web client server.\n\n" +
			"The server listens on 127.0.0.1 by default, generates (or\n" +
			"loads) an access token, and prints the URL to open.",
		Args: cobra.NoArgs,
		RunE: runServe,
	}

	// Static configuration: flag defaults come from the struct tags.
	if err := config.ApplyDefaultValues(appOptions); err != nil {
		panic(err)
	}
	if err := config.ApplyDefaultValues(terminalOptions); err != nil {
		panic(err)
	}

	var err error
	mappings, err = config.AttachFlags(serveCmd, appOptions, terminalOptions)
	if err != nil {
		panic(err)
	}

	return serveCmd
}

func runServe(cmd *cobra.Command, args []string) error {
	// Re-apply defaults for fresh option values
	if err := config.ApplyDefaultValues(appOptions); err != nil {
		return err
	}
	if err := config.ApplyDefaultValues(terminalOptions); err != nil {
		return err
	}

	// Apply configuration in precedence order:
	// env vars < config file < CLI flags
	if err := config.ApplyEnv(cmd, mappings, appOptions, terminalOptions); err != nil {
		return err
	}

	configFile, _ := cmd.Flags().GetString("config")
	_, statErr := os.Stat(utils.Expand(configFile))
	if cmd.Flags().Changed("config") || !os.IsNotExist(statErr) {
		if err := config.ApplyConfigFile(configFile, appOptions, terminalOptions); err != nil {
			return fmt.Errorf("failed to load config file: %w", err)
		}
	}

	config.ApplyFlags(cmd, mappings, appOptions, terminalOptions)

	// 服务端日志落盘(默认 ~/.gossh/logs/gossh.log,文件 + 控制台双写)
	if err := setupLogFile(appOptions.LogFile); err != nil {
		return err
	}

	appOptions.TitleVariables = map[string]interface{}{
		"hostname": mustHostname(),
	}

	// 领域对象装配
	inventory, err := host.LoadInventory(utils.Expand(appOptions.HostsFile))
	if err != nil {
		return err
	}
	log.Printf("Host inventory: %s (%d hosts)", utils.Expand(appOptions.HostsFile), len(inventory.List()))

	knownHosts, err := sshx.LoadKnownHosts(utils.Expand(appOptions.KnownHostsFile))
	if err != nil {
		return err
	}
	log.Printf("Host-key store: %s", utils.Expand(appOptions.KnownHostsFile))

	secrets := sshx.NewSecrets()
	if secrets.Available() {
		log.Printf("Credentials: system keyring available")
	} else {
		log.Printf("Credentials: no system keyring (headless?), falling back to in-memory secrets")
	}

	var store session.Store = session.NewMemoryStore()
	if appOptions.SessionFile != "" {
		storePath := utils.Expand(appOptions.SessionFile)
		fileStore, err := session.NewFileStore(storePath)
		if err != nil {
			return fmt.Errorf("failed to load session history: %w", err)
		}
		log.Printf("Session history file: %s", storePath)
		store = fileStore
	}

	manager := session.NewManager(
		session.WithMaxSession(appOptions.MaxSession),
		session.WithIdleTimeout(time.Duration(appOptions.Timeout)*time.Second),
		session.WithTerminalOptions(*terminalOptions),
		session.WithStore(store),
		session.WithMirrorFactory(api.MirrorFactory(appOptions.Mirror)),
		session.WithAnswerQueries(appOptions.AnswerQueries),
	)

	srv, err := api.New(manager, appOptions, inventory, knownHosts, secrets)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	gCtx, gCancel := context.WithCancel(context.Background())

	errs := make(chan error, 1)
	go func() {
		errs <- srv.Run(ctx, api.WithGracefullContext(gCtx))
	}()
	err = waitSignals(errs, cancel, gCancel)

	if err != nil && err != context.Canceled {
		return err
	}

	return nil
}

func mustHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

func waitSignals(errs chan error, cancel context.CancelFunc, gracefullCancel context.CancelFunc) error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(
		sigChan,
		syscall.SIGINT,
		syscall.SIGTERM,
	)

	select {
	case err := <-errs:
		return err

	case s := <-sigChan:
		switch s {
		case syscall.SIGINT:
			gracefullCancel()
			fmt.Println("C-C to force close")
			select {
			case err := <-errs:
				return err
			case <-sigChan:
				fmt.Println("Force closing...")
				cancel()
				return <-errs
			}
		default:
			cancel()
			return <-errs
		}
	}
}
