package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/acp"
	"github.com/steveyegge/gastown/internal/session"
)

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Start an interactive agent session (ACP)",
	Long: `This command initializes a headless session without tmux, designed for
the Agent Client Protocol (ACP). It acts as a streaming proxy to an
underlying ACP-supported agent.

While an ACP session is active, automatic cleanup of polecat workspaces
is deferred to avoid interrupting the session.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		agentBin := os.Getenv("GT_ACP_AGENT")
		if agentBin == "" {
			var err error
			agentBin, err = exec.LookPath("opencode")
			if err != nil {
				return fmt.Errorf("could not find 'opencode' in PATH: %w", err)
			}
		}

		startupPrompt := buildStartupPrompt()

		hasAcp, err := agentHasAcpSubcommand(agentBin)
		if err != nil {
			return fmt.Errorf("could not check for 'acp' subcommand: %w", err)
		}

		proxy := acp.NewProxy()
		proxy.SetStartupPrompt(startupPrompt)

		ctx := context.Background()
		cwd, _ := os.Getwd()

		var agentArgs []string
		if hasAcp {
			agentArgs = []string{"acp"}
		}

		if err := proxy.Start(ctx, agentBin, agentArgs, cwd); err != nil {
			return fmt.Errorf("starting agent: %w", err)
		}

		return proxy.Forward()
	},
}

func buildStartupPrompt() string {
	recipient := session.BeaconRecipient("Mayor", "", "")
	return session.FormatStartupBeacon(session.BeaconConfig{
		Recipient:               recipient,
		Sender:                  "witness",
		Topic:                   "cold-start",
		IncludePrimeInstruction: true,
	})
}

func agentHasAcpSubcommand(agentBin string) (bool, error) {
	cmd := exec.Command(agentBin, "--help")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return bytes.Contains(out, []byte("acp")), nil
}

func init() {
	mayorCmd.AddCommand(acpCmd)
}
