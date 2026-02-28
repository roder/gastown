package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/acp"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/daemon"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/mayor"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

var mayorCmd = &cobra.Command{
	Use:     "mayor",
	Aliases: []string{"may"},
	GroupID: GroupAgents,
	Short:   "Manage the Mayor (Chief of Staff for cross-rig coordination)",
	RunE:    requireSubcommand,
	Long: `Manage the Mayor - the Overseer's Chief of Staff.

The Mayor is the global coordinator for Gas Town:
  - Receives escalations from Witnesses and Deacon
  - Coordinates work across multiple rigs
  - Handles human communication when needed
  - Routes strategic decisions and cross-project issues

The Mayor is the primary interface between the human Overseer and the
automated agents. When in doubt, escalate to the Mayor.

Role shortcuts: "mayor" in mail/nudge addresses resolves to this agent.`,
}

var mayorAgentOverride string

var mayorStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Mayor session",
	Long: `Start the Mayor tmux session.

Creates a new detached tmux session for the Mayor and launches Claude.
The session runs in the workspace root directory.`,
	RunE: runMayorStart,
}

var mayorStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the Mayor session",
	Long: `Stop the Mayor tmux session.

Attempts graceful shutdown first (Ctrl-C), then kills the tmux session.`,
	RunE: runMayorStop,
}

var mayorAttachCmd = &cobra.Command{
	Use:     "attach",
	Aliases: []string{"at"},
	Short:   "Attach to the Mayor session",
	Long: `Attach to the running Mayor tmux session.

Attaches the current terminal to the Mayor's tmux session.
Detach with Ctrl-B D.`,
	RunE: runMayorAttach,
}

var mayorStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check Mayor session status",
	Long:  `Check if the Mayor tmux session is currently running.`,
	RunE:  runMayorStatus,
}

var mayorRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the Mayor session",
	Long: `Restart the Mayor tmux session.

Stops the current session (if running) and starts a fresh one.`,
	RunE: runMayorRestart,
}

var mayorAcpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Run Mayor in headless mode (Agent Control Protocol)",
	Long: `Run the Mayor in headless mode with stdin/stdout connected.

This command initializes a headless session without tmux, designed for
IDE integration via the Agent Control Protocol. It bypasses all tmux
logic and runs directly in the current terminal.

Environment variable overrides:
  GT_RIG          - Override rig name
  GT_TOWN_ROOT    - Override town root directory
  GT_ROLE         - Override role (default: mayor)

The agent reads prompts from stdin and outputs to stdout. This enables
programmatic control by IDEs or other tools that need direct agent access.

While an ACP session is active, automatic cleanup of polecat workspaces
is vetoed to allow the Mayor to review worker diffs before they vanish.`,
	RunE: runMayorAcp,
}

var acpRigOverride string
var acpTownRootOverride string

func init() {
	mayorCmd.AddCommand(mayorStartCmd)
	mayorCmd.AddCommand(mayorStopCmd)
	mayorCmd.AddCommand(mayorAttachCmd)
	mayorCmd.AddCommand(mayorStatusCmd)
	mayorCmd.AddCommand(mayorRestartCmd)
	mayorCmd.AddCommand(mayorAcpCmd)

	mayorStartCmd.Flags().StringVar(&mayorAgentOverride, "agent", "", "Agent alias to run the Mayor with (overrides town default)")
	mayorAttachCmd.Flags().StringVar(&mayorAgentOverride, "agent", "", "Agent alias to run the Mayor with (overrides town default)")
	mayorRestartCmd.Flags().StringVar(&mayorAgentOverride, "agent", "", "Agent alias to run the Mayor with (overrides town default)")

	mayorAcpCmd.Flags().StringVar(&acpRigOverride, "rig", "", "Rig name (overrides GT_RIG env)")
	mayorAcpCmd.Flags().StringVar(&acpTownRootOverride, "town", "", "Town root directory (overrides GT_TOWN_ROOT env)")
	mayorAcpCmd.Flags().StringVar(&mayorAgentOverride, "agent", "", "Agent alias to run (overrides town default)")

	rootCmd.AddCommand(mayorCmd)
}

// getMayorManager returns a mayor manager for the current workspace.
func getMayorManager() (*mayor.Manager, error) {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return nil, fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	return mayor.NewManager(townRoot), nil
}

// getMayorSessionName returns the Mayor session name.
func getMayorSessionName() string {
	return mayor.SessionName()
}

func runMayorStart(cmd *cobra.Command, args []string) error {
	mgr, err := getMayorManager()
	if err != nil {
		return err
	}

	fmt.Println("Starting Mayor session...")
	if err := mgr.Start(mayorAgentOverride); err != nil {
		if err == mayor.ErrAlreadyRunning {
			return fmt.Errorf("Mayor session already running. Attach with: gt mayor attach")
		}
		return err
	}

	fmt.Printf("%s Mayor session started. Attach with: %s\n",
		style.Bold.Render("✓"),
		style.Dim.Render("gt mayor attach"))

	return nil
}

func runMayorStop(cmd *cobra.Command, args []string) error {
	mgr, err := getMayorManager()
	if err != nil {
		return err
	}

	fmt.Println("Stopping Mayor session...")
	if err := mgr.Stop(); err != nil {
		if err == mayor.ErrNotRunning {
			return fmt.Errorf("Mayor session is not running")
		}
		return err
	}

	fmt.Printf("%s Mayor session stopped.\n", style.Bold.Render("✓"))
	return nil
}

func runMayorAttach(cmd *cobra.Command, args []string) error {
	mgr, err := getMayorManager()
	if err != nil {
		return err
	}

	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("finding workspace: %w", err)
	}

	// Ensure daemon and dolt are running before attaching.
	ensureMayorInfra(townRoot)

	t := tmux.NewTmux()
	sessionID := mgr.SessionName()

	running, err := mgr.IsRunning()
	if err != nil {
		return fmt.Errorf("checking session: %w", err)
	}
	if !running {
		// Auto-start if not running
		fmt.Println("Mayor session not running, starting...")
		if err := mgr.Start(mayorAgentOverride); err != nil {
			return err
		}
	} else {
		// Session exists - check if runtime is still running (hq-95xfq, gt-7zl)
		// If runtime exited or sitting at shell, restart with proper context.
		// Use IsAgentAlive (checks descendant processes) instead of IsAgentRunning
		// (pane command only), since mayor launches via bash wrapper.
		if !t.IsAgentAlive(sessionID) {
			// Runtime has exited, restart it with proper context
			fmt.Println("Runtime exited, restarting with context...")

			paneID, err := t.GetPaneID(sessionID)
			if err != nil {
				return fmt.Errorf("getting pane ID: %w", err)
			}

			// Build startup beacon for context (like gt handoff does)
			beacon := session.FormatStartupBeacon(session.BeaconConfig{
				Recipient: "mayor",
				Sender:    "human",
				Topic:     "attach",
			})

			// Build startup command with beacon
			startupCmd, err := config.BuildAgentStartupCommandWithAgentOverride("mayor", "", townRoot, "", beacon, mayorAgentOverride)
			if err != nil {
				return fmt.Errorf("building startup command: %w", err)
			}

			// Set remain-on-exit so the pane survives process death during respawn.
			// Without this, killing processes causes tmux to destroy the pane.
			if err := t.SetRemainOnExit(paneID, true); err != nil {
				style.PrintWarning("could not set remain-on-exit: %v", err)
			}

			// Kill all processes in the pane before respawning to prevent orphan leaks
			// RespawnPane's -k flag only sends SIGHUP which Claude/Node may ignore
			if err := t.KillPaneProcesses(paneID); err != nil {
				// Non-fatal but log the warning
				style.PrintWarning("could not kill pane processes: %v", err)
			}

			// Note: respawn-pane automatically resets remain-on-exit to off
			if err := t.RespawnPane(paneID, startupCmd); err != nil {
				return fmt.Errorf("restarting runtime: %w", err)
			}

			fmt.Printf("%s Mayor restarted with context\n", style.Bold.Render("✓"))
		}
	}

	// Use shared attach helper (smart: links if inside tmux, attaches if outside)
	return attachToTmuxSession(sessionID)
}

func runMayorStatus(cmd *cobra.Command, args []string) error {
	mgr, err := getMayorManager()
	if err != nil {
		return err
	}

	info, err := mgr.Status()
	if err != nil {
		if err == mayor.ErrNotRunning {
			fmt.Printf("%s Mayor session is %s\n",
				style.Dim.Render("○"),
				"not running")
			fmt.Printf("\nStart with: %s\n", style.Dim.Render("gt mayor start"))
			return nil
		}
		return fmt.Errorf("checking status: %w", err)
	}

	status := "detached"
	if info.Attached {
		status = "attached"
	}
	fmt.Printf("%s Mayor session is %s\n",
		style.Bold.Render("●"),
		style.Bold.Render("running"))
	fmt.Printf("  Status: %s\n", status)
	fmt.Printf("  Created: %s\n", info.Created)
	fmt.Printf("\nAttach with: %s\n", style.Dim.Render("gt mayor attach"))

	return nil
}

func runMayorRestart(cmd *cobra.Command, args []string) error {
	mgr, err := getMayorManager()
	if err != nil {
		return err
	}

	// Stop if running (ignore not-running error)
	if err := mgr.Stop(); err != nil && err != mayor.ErrNotRunning {
		return fmt.Errorf("stopping session: %w", err)
	}

	// Start fresh
	return runMayorStart(cmd, args)
}

// ensureMayorInfra checks that daemon and dolt are running before attaching
// to the Mayor session. Warns and auto-starts each if absent. Non-fatal:
// failures are reported but do not block the attach.
func ensureMayorInfra(townRoot string) {
	// Load daemon.json env vars (e.g., GT_DOLT_PORT) so Dolt uses the right port.
	if patrolCfg := daemon.LoadPatrolConfig(townRoot); patrolCfg != nil {
		for k, v := range patrolCfg.Env {
			os.Setenv(k, v)
		}
	}

	// Daemon
	daemonRunning, _, _ := daemon.IsRunning(townRoot)
	if !daemonRunning {
		style.PrintWarning("daemon is not running, starting...")
		if err := ensureDaemon(townRoot); err != nil {
			style.PrintWarning("daemon start failed: %v", err)
		} else {
			fmt.Printf("  %s Daemon started\n", style.Bold.Render("✓"))
		}
	}

	// Dolt (skip if no local data dir or if server is remote)
	doltCfg := doltserver.DefaultConfig(townRoot)
	if !doltCfg.IsRemote() {
		if _, err := os.Stat(doltCfg.DataDir); err == nil {
			doltRunning, _, _ := doltserver.IsRunning(townRoot)
			if !doltRunning {
				style.PrintWarning("Dolt server is not running, starting...")
				if err := doltserver.Start(townRoot); err != nil {
					style.PrintWarning("Dolt server start failed: %v", err)
				} else {
					fmt.Printf("  %s Dolt server started (port %d)\n", style.Bold.Render("✓"), doltCfg.Port)
				}
			}
		}
	}
}

// runMayorAcp runs the Mayor in headless mode for IDE integration.
// It bypasses tmux and execs the agent directly with stdin/stdout connected.
// A PID file is created to signal that automatic cleanup should be vetoed,
// allowing the Mayor to review worker diffs before cleanup.
func runMayorAcp(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	townRoot := acpTownRootOverride
	if townRoot == "" {
		townRoot = os.Getenv("GT_TOWN_ROOT")
	}
	if townRoot == "" {
		var err error
		townRoot, err = workspace.FindFromCwdOrError()
		if err != nil {
			return fmt.Errorf("not in a Gas Town workspace: %w", err)
		}
	}

	ensureMayorInfra(townRoot)

	rigName := acpRigOverride
	if rigName == "" {
		rigName = os.Getenv("GT_RIG")
	}

	_, agentName, err := config.ResolveAgentConfigWithOverride(townRoot, "", mayorAgentOverride)
	if err != nil {
		return fmt.Errorf("resolving agent config: %w", err)
	}

	if !config.SupportsACP(agentName) {
		return fmt.Errorf("agent '%s' does not support ACP. Use an ACP-compatible agent like 'opencode'.", agentName)
	}

	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:     "mayor",
		Rig:      rigName,
		TownRoot: townRoot,
	})
	for k, v := range envVars {
		os.Setenv(k, v)
	}

	mayorDir := filepath.Join(townRoot, "mayor")
	if err := os.Chdir(mayorDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not cd to mayor directory: %v\n", err)
	}

	if err := mayor.WriteACPPid(townRoot); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write ACP PID file: %v\n", err)
	}
	defer func() {
		if err := mayor.RemoveACPPid(townRoot); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not remove ACP PID file: %v\n", err)
		}
	}()

	proxy := acp.NewProxy()

	beacon := session.FormatStartupBeacon(session.BeaconConfig{
		Recipient: "mayor",
		Sender:    "human",
		Topic:     "acp",
	})
	proxy.SetStartupPrompt(beacon)

	acpConfig := config.GetACPConfig(agentName)
	var agentArgs []string
	if acpConfig != nil && acpConfig.Command != "" {
		agentArgs = []string{acpConfig.Command}
		if len(acpConfig.Args) > 0 {
			agentArgs = append(agentArgs, acpConfig.Args...)
		}
	}

	if err := proxy.Start(ctx, agentName, agentArgs, mayorDir); err != nil {
		return fmt.Errorf("starting agent: %w", err)
	}

	return proxy.Forward()
}
