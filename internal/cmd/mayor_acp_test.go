package cmd

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestAcpCmd(t *testing.T) {
	// 1. Create a mock agent that reads from stdin and writes to stdout.
	mockAgent, err := createMockAgent()
	if err != nil {
		t.Fatalf("could not create mock agent: %v", err)
	}
	defer os.Remove(mockAgent)

	// 2. Create a pipe for the user's stdin.
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("could not create stdin pipe: %v", err)
	}
	defer stdinReader.Close()
	defer stdinWriter.Close()

	// 3. Create a buffer for the agent's stdout.
	var stdout bytes.Buffer

	// 4. Create a new acpCmd with the mock agent and the pipes.
	cmd := acpCmd
	cmd.SetArgs([]string{})
	cmd.SetIn(stdinReader)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	// 5. Run the command in a separate goroutine.
	go func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("acpCmd returned an error: %v", err)
		}
	}()

	// 6. Write to the user's stdin.
	if _, err := io.WriteString(stdinWriter, "test input
"); err != nil {
		t.Fatalf("could not write to stdin pipe: %v", err)
	}

	// 7. Wait for the agent to process the input.
	time.Sleep(1 * time.Second)

	// 8. Check the agent's stdout.
	expected := `{"jsonrpc": "2.0", "method": "initialize", "params": {}}` + "
" + "test input
"
	if stdout.String() != expected {
		t.Errorf("expected stdout to be %q, but got %q", expected, stdout.String())
	}
}

func createMockAgent() (string, error) {
	// Create a temporary file for the mock agent.
	f, err := os.CreateTemp("", "mock-agent-")
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Write the mock agent script to the file.
	script := `#!/bin/bash
read -r line
echo "$line"
cat
`
	if _, err := io.WriteString(f, script); err != nil {
		return "", err
	}

	// Make the file executable.
	if err := os.Chmod(f.Name(), 0755); err != nil {
		return "", err
	}

	return f.Name(), nil
}
