# Agent Instructions

See **CLAUDE.md** for complete agent context and instructions.

This file exists for compatibility with tools that look for AGENTS.md.

> **Recovery**: Run `gt prime` after compaction, clear, or new session

Full context is injected by `gt prime` at session start.

<!-- beads-agent-instructions-v2 -->

---

## Beads Workflow Integration

This project uses [beads](https://github.com/steveyegge/beads) for issue tracking. Issues live in `.beads/` and are tracked in git.

Two CLIs: **bd** (issue CRUD) and **bv** (graph-aware triage, read-only).

### bd: Issue Management

```bash
bd ready              # Unblocked issues ready to work
bd list --status=open # All open issues
bd show <id>          # Full details with dependencies
bd create --title="..." --type=task --priority=2
bd update <id> --status=in_progress
bd close <id>         # Mark complete
bd close <id1> <id2>  # Close multiple
bd dep add <a> <b>    # a depends on b
bd sync               # Sync with git
```

### bv: Graph Analysis (read-only)

**NEVER run bare `bv`** — it launches interactive TUI. Always use `--robot-*` flags:

```bash
bv --robot-triage     # Ranked picks, quick wins, blockers, health
bv --robot-next       # Single top pick + claim command
bv --robot-plan       # Parallel execution tracks
bv --robot-alerts     # Stale issues, cascades, mismatches
bv --robot-insights   # Full graph metrics: PageRank, betweenness, cycles
```

### Workflow

1. **Start**: `bd ready` (or `bv --robot-triage` for graph analysis)
2. **Claim**: `bd update <id> --status=in_progress`
3. **Work**: Implement the task
4. **Complete**: `bd close <id>`
5. **Sync**: `bd sync` at session end

### Session Close Protocol

```bash
git status            # Check what changed
git add <files>       # Stage code changes
bd sync               # Commit beads changes
git commit -m "..."   # Commit code
bd sync               # Commit any new beads changes
git push              # Push to remote
```

### Key Concepts

- **Priority**: P0=critical, P1=high, P2=medium, P3=low, P4=backlog (numbers only)
- **Types**: task, bug, feature, epic, question, docs
- **Dependencies**: `bd ready` shows only unblocked work

<!-- end-beads-agent-instructions -->

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds

---

## ACP: Agent Communication Protocol

Gas Town supports running multiple agents simultaneously via the **Agent Communication Protocol (ACP)**. This enables coordinated multi-agent workflows where agents can communicate and share context.

### Running the Mayor in Headless Mode

The Mayor can run in headless mode using ACP for IDE integration:

```bash
gt mayor acp                    # Run with default agent
gt mayor acp --agent opencode   # Run with specific agent
gt mayor acp --rig gastown      # Specify rig name
```

**Requirements:**
- The agent must support ACP (currently `opencode` is ACP-compatible)
- ACP creates a PID file to prevent automatic cleanup during active sessions
- While an ACP session is active, workspace cleanup is deferred to allow the Mayor to review worker diffs

### ACP-Compatible Agents

Configure an agent with `ACPSubcommand` to enable ACP support:

```json
{
  "agents": {
    "opencode": {
      "acp_subcommand": "acp"
    }
  }
}
```

---

## Hooks and Adapter Installation

Gas Town uses a hook system to integrate with agent runtimes. Hooks provide lifecycle events and instructions to agents.

### Installing Hooks

Browse and install hooks from the registry:

```bash
gt hooks list                           # List available hooks
gt hooks registry                       # Show registry details
gt hooks install <hook-id>              # Install to current worktree
gt hooks install <hook-id> --role crew  # Install to all crew in current rig
gt hooks install <hook-id> --all-rigs   # Install across all rigs (requires --role)
gt hooks install <hook-id> --dry-run    # Preview without writing
```

### Hook Providers

Each agent can define a hooks provider that controls how hooks are installed:

| Provider   | Settings File     | Description                      |
|------------|-------------------|----------------------------------|
| claude     | settings.json     | Claude Code hooks                |
| opencode   | gastown.js        | OpenCode plugin hooks            |
| copilot    | copilot-instructions.md | Copilot instructions    |
| pi         | gastown-hooks.js  | Pi agent extensions              |

### Registering Custom Hook Installers

For new agent integrations, register a hook installer function:

```go
config.RegisterHookInstaller("myagent", func(worktreePath string, hookDef config.HookDefinition) error {
    // Write hook files to worktree
})
```
