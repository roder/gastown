package cmd

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestAgentHasAcpSubcommand(t *testing.T) {
	mockAgent, err := createMockAgent()
	if err != nil {
		t.Fatalf("could not create mock agent: %v", err)
	}
	defer os.Remove(mockAgent)

	hasAcp, err := agentHasAcpSubcommand(mockAgent)
	if err != nil {
		t.Fatalf("agentHasAcpSubcommand returned error: %v", err)
	}

	if hasAcp {
		t.Error("expected hasAcp to be false for mock agent without acp in help")
	}
}

func TestAgentHasAcpSubcommand_WithAcp(t *testing.T) {
	mockAgent, err := createMockAgentWithAcp()
	if err != nil {
		t.Fatalf("could not create mock agent: %v", err)
	}
	defer os.Remove(mockAgent)

	hasAcp, err := agentHasAcpSubcommand(mockAgent)
	if err != nil {
		t.Fatalf("agentHasAcpSubcommand returned error: %v", err)
	}

	if !hasAcp {
		t.Error("expected hasAcp to be true for mock agent with acp in help")
	}
}

func createMockAgent() (string, error) {
	f, err := os.CreateTemp("", "mock-agent-")
	if err != nil {
		return "", err
	}
	defer f.Close()

	script := `#!/bin/bash
echo "Usage: mock-agent [options]"
echo "Options:"
echo "  --help    Show help"
`
	if _, err := io.WriteString(f, script); err != nil {
		return "", err
	}

	if err := os.Chmod(f.Name(), 0755); err != nil {
		return "", err
	}

	return f.Name(), nil
}

func createMockAgentWithAcp() (string, error) {
	f, err := os.CreateTemp("", "mock-agent-acp-")
	if err != nil {
		return "", err
	}
	defer f.Close()

	script := `#!/bin/bash
echo "Usage: mock-agent [command]"
echo "Commands:"
echo "  acp       Start ACP session"
echo "  --help    Show help"
`
	if _, err := io.WriteString(f, script); err != nil {
		return "", err
	}

	if err := os.Chmod(f.Name(), 0755); err != nil {
		return "", err
	}

	return f.Name(), nil
}

func TestBytesContains(t *testing.T) {
	out := []byte("Usage: test\nCommands:\n  acp       Start ACP\n")
	if !bytes.Contains(out, []byte("acp")) {
		t.Error("expected bytes.Contains to find 'acp'")
	}
}
