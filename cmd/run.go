package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/sshx"
	"github.com/gausszhou/gossh/internal/utils"

	"golang.org/x/crypto/ssh"
)

// buildRunCmd creates `gossh run <host> <command...>` — run a command on
// a host from the inventory without opening the browser.
func buildRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <host-id|host-name> <command...>",
		Short: "Run a command on a host (no browser needed)",
		Args:  cobra.MinimumNArgs(2),
		RunE:  runRun,
	}
	cmd.Flags().String("hosts-file", defaultHostsFile(), "Host inventory file path")
	cmd.Flags().String("known-hosts-file", "~/.gossh/known_hosts", "TOFU host-key store path")
	cmd.Flags().Int("timeout", 0, "Command timeout in seconds (0 = default 60s, -1 = none)")
	cmd.Flags().String("password", "", "Password for this connect (overrides keyring)")
	cmd.Flags().String("passphrase", "", "Passphrase for this connect (overrides keyring)")
	return cmd
}

func runRun(cmd *cobra.Command, args []string) error {
	hostsFile, _ := cmd.Flags().GetString("hosts-file")
	khFile, _ := cmd.Flags().GetString("known-hosts-file")
	timeoutSec, _ := cmd.Flags().GetInt("timeout")
	password, _ := cmd.Flags().GetString("password")
	passphrase, _ := cmd.Flags().GetString("passphrase")

	inv, err := host.LoadInventory(utils.Expand(hostsFile))
	if err != nil {
		return err
	}
	kh, err := sshx.LoadKnownHosts(utils.Expand(khFile))
	if err != nil {
		return err
	}
	secrets := sshx.NewSecrets()

	// resolve <id|name>
	hostID := args[0]
	hosts := inv.List()
	found := false
	for _, h := range hosts {
		if h.ID == hostID || h.Name == hostID {
			hostID = h.ID
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no host matches %q", args[0])
	}

	prov := &sshx.ProvidedSecrets{}
	if password != "" {
		prov.Password = &password
	}
	if passphrase != "" {
		prov.Passphrase = &passphrase
	}

	chain, err := inv.Chain(hostID)
	if err != nil {
		return err
	}
	hops, err := sshx.BuildHops(chain, secrets, prov, kh, 10*time.Second)
	if err != nil {
		return err
	}

	ctx := context.Background()
	cancel := func() {}
	if timeoutSec >= 0 {
		d := 60 * time.Second
		if timeoutSec > 0 {
			d = time.Duration(timeoutSec) * time.Second
		}
		ctx, cancel = context.WithTimeout(ctx, d)
	}
	defer cancel()

	dial, err := sshx.DialChain(ctx, hops)
	if err != nil {
		return err
	}
	defer dial.Close()

	sess, err := dial.Target.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	command := strings.Join(args[1:], " ")
	output, runErr := sess.CombinedOutput(command)

	if _, err := os.Stderr.Write(output); err != nil {
		return err
	}

	if runErr != nil {
		var ee *ssh.ExitError
		if errors.As(runErr, &ee) {
			os.Exit(ee.ExitStatus())
		}
		return fmt.Errorf("command failed: %w", runErr)
	}
	return nil
}
