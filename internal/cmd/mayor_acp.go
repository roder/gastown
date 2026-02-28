package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
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
		// 1. Determine the agent binary to use.
		// This should be configurable, but for now, we'll hardcode "opencode".
		agentBin, err := exec.LookPath("opencode")
		if err != nil {
			return fmt.Errorf("could not find 'opencode' in PATH: %w", err)
		}

		// 2. Check if the agent has an "acp" subcommand.
		hasAcp, err := agentHasAcpSubcommand(agentBin)
		if err != nil {
			return fmt.Errorf("could not check for 'acp' subcommand: %w", err)
		}

		// 3. Get the Prime Directive (Formula)
		// This is a placeholder for the actual logic to get the startup beacon.
		// The real implementation would call session.FormatStartupBeacon().
		formula := "You are the Gastown Mayor, the Overseer's Chief of Staff."

		// 4. Construct the command based on whether the agent has an "acp" subcommand.
		var c *exec.Cmd
		ctx := context.Background()
		if hasAcp {
			c = exec.CommandContext(ctx, agentBin, "acp", "--prompt", formula)
		} else {
			// "Ghost" Identity fallback
			identityPrompt := fmt.Sprintf("[IDENTITY OVERRIDE]: %s", formula)
			c = exec.CommandContext(ctx, agentBin, "--prompt", identityPrompt)
		}

		// 5. Create a pipe for the agent's stdin.
		stdin, err := c.StdinPipe()
		if err != nil {
			return fmt.Errorf("could not create stdin pipe: %w", err)
		}

		// 6. Connect the agent's stdout and stderr.
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr

		// 7. Start the agent.
		if err := c.Start(); err != nil {
			return fmt.Errorf("could not start agent: %w", err)
		}

		// 8. Write the initialize message to the agent's stdin.
		// This is a placeholder for the actual JSON-RPC message.
		initMessage := `{"jsonrpc": "2.0", "method": "initialize", "params": {}}`
		if _, err := io.WriteString(stdin, initMessage+"\n"); err != nil {
			return fmt.Errorf("could not write initialize message: %w", err)
		}

		// 9. Copy the user's stdin to the agent's stdin in a separate goroutine.
		go func() {
			defer stdin.Close()
			io.Copy(stdin, os.Stdin)
		}()

		// 10. Wait for the command to complete.
		return c.Wait()
	},
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
