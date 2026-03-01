package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestNewProxy(t *testing.T) {
	p := NewProxy()
	if p == nil {
		t.Fatal("NewProxy returned nil")
	}
	if p.done == nil {
		t.Error("done channel not initialized")
	}
}

func TestProxy_SessionID(t *testing.T) {
	p := NewProxy()

	if sid := p.SessionID(); sid != "" {
		t.Errorf("expected empty session ID, got %q", sid)
	}

	p.sessionMux.Lock()
	p.sessionID = "test-session-123"
	p.sessionMux.Unlock()

	if sid := p.SessionID(); sid != "test-session-123" {
		t.Errorf("expected session ID %q, got %q", "test-session-123", sid)
	}
}

func TestProxy_ExtractSessionID(t *testing.T) {
	tests := []struct {
		name    string
		msg     *JSONRPCMessage
		wantSID string
	}{
		{
			name: "session/new response with sessionId",
			msg: &JSONRPCMessage{
				ID:     1,
				Result: json.RawMessage(`{"sessionId":"sess_abc123"}`),
			},
			wantSID: "sess_abc123",
		},
		{
			name: "response without sessionId",
			msg: &JSONRPCMessage{
				ID:     2,
				Result: json.RawMessage(`{"other":"field"}`),
			},
			wantSID: "",
		},
		{
			name: "notification without ID",
			msg: &JSONRPCMessage{
				Method: "session/update",
				Params: json.RawMessage(`{"sessionId":"sess_xyz"}`),
			},
			wantSID: "",
		},
		{
			name: "request without result",
			msg: &JSONRPCMessage{
				ID:     3,
				Method: "session/prompt",
			},
			wantSID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewProxy()
			p.extractSessionID(tt.msg)

			if sid := p.SessionID(); sid != tt.wantSID {
				t.Errorf("expected session ID %q, got %q", tt.wantSID, sid)
			}
		})
	}
}

func TestProxy_InjectNotification(t *testing.T) {
	p := NewProxy()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	p.stdin = w

	p.sessionMux.Lock()
	p.sessionID = "test-session"
	p.sessionMux.Unlock()

	go func() {
		_ = p.InjectNotification("session/update", map[string]any{"status": "working"})
		w.Close()
	}()

	var buf bytes.Buffer
	io.Copy(&buf, r)

	var msg JSONRPCMessage
	if err := json.Unmarshal(buf.Bytes(), &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %q", msg.JSONRPC)
	}
	if msg.Method != "session/update" {
		t.Errorf("expected method session/update, got %q", msg.Method)
	}

	var params map[string]any
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		t.Fatalf("failed to parse params: %v", err)
	}
	if params["sessionId"] != "test-session" {
		t.Errorf("expected sessionId test-session, got %v", params["sessionId"])
	}
	if params["status"] != "working" {
		t.Errorf("expected status working, got %v", params["status"])
	}
}

func TestProxy_Shutdown(t *testing.T) {
	p := NewProxy()

	_, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	p.done = make(chan struct{})

	p.Shutdown()

	select {
	case <-p.done:
	case <-time.After(100 * time.Millisecond):
		t.Error("done channel not closed after shutdown")
	}
}

func TestProxy_MarkDone(t *testing.T) {
	p := NewProxy()
	p.done = make(chan struct{})

	p.markDone()
	p.markDone()

	select {
	case <-p.done:
	default:
		t.Error("done channel not closed after markDone")
	}
}

func TestJSONRPCMessage_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		msg  JSONRPCMessage
	}{
		{
			name: "request",
			msg: JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "initialize",
				Params:  json.RawMessage(`{"protocolVersion":1}`),
			},
		},
		{
			name: "response with result",
			msg: JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      1,
				Result:  json.RawMessage(`{"sessionId":"sess_123"}`),
			},
		},
		{
			name: "response with error",
			msg: JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      1,
				Error: &JSONRPCError{
					Code:    -32600,
					Message: "Invalid Request",
				},
			},
		},
		{
			name: "notification",
			msg: JSONRPCMessage{
				JSONRPC: "2.0",
				Method:  "session/update",
				Params:  json.RawMessage(`{"sessionId":"sess_123"}`),
			},
		},
		{
			name: "string id",
			msg: JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      "abc-123",
				Method:  "session/prompt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(&tt.msg)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var got JSONRPCMessage
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if got.JSONRPC != tt.msg.JSONRPC {
				t.Errorf("jsonrpc mismatch: got %q, want %q", got.JSONRPC, tt.msg.JSONRPC)
			}

			if tt.msg.Method != "" && got.Method != tt.msg.Method {
				t.Errorf("method mismatch: got %q, want %q", got.Method, tt.msg.Method)
			}
		})
	}
}

func TestProxy_StartProcessGroup(t *testing.T) {
	p := NewProxy()

	ctx := context.Background()
	p.cmd = p.createTestCmd(ctx)

	if p.cmd.SysProcAttr == nil {
		t.Error("SysProcAttr should be set for process group control")
	}
}

func (p *Proxy) createTestCmd(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd
}

func TestProxy_ConcurrentSessionID(t *testing.T) {
	p := NewProxy()

	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		go func(id int) {
			defer wg.Done()
			p.sessionMux.Lock()
			p.sessionID = fmt.Sprintf("session-%d", id)
			p.sessionMux.Unlock()
		}(i)

		go func() {
			defer wg.Done()
			_ = p.SessionID()
		}()
	}

	wg.Wait()
}

func TestProxy_InjectNotification_NoSessionID(t *testing.T) {
	p := NewProxy()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	p.stdin = w

	go func() {
		_ = p.InjectNotification("test/notification", nil)
		w.Close()
	}()

	var buf bytes.Buffer
	io.Copy(&buf, r)

	if buf.Len() == 0 {
		t.Fatal("expected message to be written")
	}

	var msg JSONRPCMessage
	if err := json.Unmarshal(buf.Bytes(), &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Method != "test/notification" {
		t.Errorf("expected method test/notification, got %q", msg.Method)
	}
}

func TestProxy_InjectNotification_WithParams(t *testing.T) {
	p := NewProxy()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	p.stdin = w

	p.sessionMux.Lock()
	p.sessionID = "sess-test"
	p.sessionMux.Unlock()

	go func() {
		_ = p.InjectNotification("custom/method", map[string]any{
			"key1": "value1",
			"key2": 42,
		})
		w.Close()
	}()

	var buf bytes.Buffer
	io.Copy(&buf, r)

	var msg JSONRPCMessage
	if err := json.Unmarshal(buf.Bytes(), &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	var params map[string]any
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		t.Fatalf("failed to parse params: %v", err)
	}

	if params["sessionId"] != "sess-test" {
		t.Errorf("expected sessionId sess-test, got %v", params["sessionId"])
	}
	if params["key1"] != "value1" {
		t.Errorf("expected key1 value1, got %v", params["key1"])
	}
}

func TestProxy_AgentDone(t *testing.T) {
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "true")

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start command: %v", err)
	}

	p := &Proxy{cmd: cmd}

	done := p.agentDone()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("command failed: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for command to complete")
	}
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

func TestJSONRPCError(t *testing.T) {
	rpcErr := &JSONRPCError{
		Code:    -32600,
		Message: "Invalid Request",
	}

	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Error:   rpcErr,
	}

	data, err := json.Marshal(&msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	if !strings.Contains(string(data), `"code":-32600`) {
		t.Error("error code not in marshaled output")
	}
	if !strings.Contains(string(data), `"message":"Invalid Request"`) {
		t.Error("error message not in marshaled output")
	}
}

func TestIntegration_HandshakeSequence(t *testing.T) {
	p := NewProxy()

	initResp := &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Result:  json.RawMessage(`{"protocolVersion":1,"capabilities":{}}`),
	}
	p.extractSessionID(initResp)

	if sid := p.SessionID(); sid != "" {
		t.Errorf("expected empty session ID after init, got %q", sid)
	}

	sessionResp := &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      2,
		Result:  json.RawMessage(`{"sessionId":"test-session-12345"}`),
	}
	p.extractSessionID(sessionResp)

	if sid := p.SessionID(); sid != "test-session-12345" {
		t.Errorf("expected session ID test-session-12345, got %q", sid)
	}
}

func TestIntegration_StartupPromptInjection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	mockAgent := createMockACPAgent(t, true)
	defer os.Remove(mockAgent)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p := NewProxy()
	testPrompt := "GAS TOWN INTEGRATION TEST PROMPT"
	p.SetStartupPrompt(testPrompt)

	tmpDir := t.TempDir()
	if err := p.Start(ctx, mockAgent, nil, tmpDir); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}

	go func() {
		time.Sleep(200 * time.Millisecond)
		p.Shutdown()
	}()

	_ = p.Forward()

	if p.getStartupPrompt() != testPrompt {
		t.Errorf("startup prompt not set correctly")
	}
}

func TestIntegration_PropulsionNotificationFormat(t *testing.T) {
	p := NewProxy()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	p.stdin = w

	p.sessionMux.Lock()
	p.sessionID = "test-session-propulsion"
	p.sessionMux.Unlock()

	propulsionParams := map[string]any{
		"role":    "polecat",
		"rig":     "gastown",
		"message": "Polecat nux checking in",
	}

	go func() {
		_ = p.InjectNotification("session/update", propulsionParams)
		w.Close()
	}()

	var buf bytes.Buffer
	io.Copy(&buf, r)

	var msg JSONRPCMessage
	if err := json.Unmarshal(buf.Bytes(), &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Method != "session/update" {
		t.Errorf("expected method session/update, got %q", msg.Method)
	}

	var params map[string]any
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		t.Fatalf("failed to parse params: %v", err)
	}

	if params["sessionId"] != "test-session-propulsion" {
		t.Errorf("expected sessionId test-session-propulsion, got %v", params["sessionId"])
	}
	if params["role"] != "polecat" {
		t.Errorf("expected role polecat, got %v", params["role"])
	}
}

func TestIntegration_CleanExitOnAgentTermination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	exitScript := createTempScript(t, "#!/bin/sh\nexit 0\n")
	defer os.Remove(exitScript)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p := NewProxy()

	tmpDir := t.TempDir()
	if err := p.Start(ctx, exitScript, nil, tmpDir); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	agentDone := p.agentDone()
	select {
	case err := <-agentDone:
		if err != nil {
			t.Errorf("agent exited with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for agent to terminate")
	}

	p.markDone()
}

func TestIntegration_NonACPAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	nonACPAgent := createTempScript(t, "#!/bin/sh\necho 'not jsonrpc'\nexit 0\n")
	defer os.Remove(nonACPAgent)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p := NewProxy()

	tmpDir := t.TempDir()
	if err := p.Start(ctx, nonACPAgent, nil, tmpDir); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- p.Forward()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Logf("non-ACP agent returned error: %v (expected)", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for non-ACP agent")
	}

	if sid := p.SessionID(); sid != "" {
		t.Errorf("expected empty session ID for non-ACP agent, got %q", sid)
	}
}

func createMockACPAgent(t *testing.T, validACP bool) string {
	t.Helper()

	var script string
	if validACP {
		script = `#!/bin/sh
# Mock ACP agent for integration testing
while IFS= read -r line; do
    # Parse the JSON-RPC request
    method=$(echo "$line" | grep -o '"method":"[^"]*"' | cut -d'"' -f4)
    id=$(echo "$line" | grep -o '"id":[0-9]*' | cut -d: -f2)
    
    case "$method" in
        initialize)
            echo '{"jsonrpc":"2.0","id":'$id',"result":{"protocolVersion":1,"capabilities":{}}}'
            ;;
        session/new)
            echo '{"jsonrpc":"2.0","id":'$id',"result":{"sessionId":"test-session-12345"}}'
            ;;
        session/prompt)
            echo '{"jsonrpc":"2.0","id":'$id',"result":{}}'
            ;;
        *)
            echo '{"jsonrpc":"2.0","id":'$id',"result":{}}'
            ;;
    esac
done
`
	} else {
		script = "#!/bin/sh\necho 'not a valid ACP response'\n"
	}

	return createTempScript(t, script)
}

func createTempScript(t *testing.T, content string) string {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "mock-agent-*.sh")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("failed to close temp file: %v", err)
	}

	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		t.Fatalf("failed to chmod script: %v", err)
	}

	return tmpFile.Name()
}

func TestProxy_HandleToolCallNotification(t *testing.T) {
	tests := []struct {
		name           string
		inputMsg       *JSONRPCMessage
		expectUpdate   bool
		expectedToolID string
	}{
		{
			name: "ToolCall with content triggers ToolCallUpdate",
			inputMsg: &JSONRPCMessage{
				JSONRPC: "2.0",
				Method:  "session/update",
				Params:  json.RawMessage(`{"sessionId":"test-session","update":{"ToolCall":{"tool_call_id":"tool-123","fields":{"content":[{"type":"text","text":"command output"}],"status":"in_progress"}}}}`),
			},
			expectUpdate:   true,
			expectedToolID: "tool-123",
		},
		{
			name: "ToolCall without content does not trigger update",
			inputMsg: &JSONRPCMessage{
				JSONRPC: "2.0",
				Method:  "session/update",
				Params:  json.RawMessage(`{"sessionId":"test-session","update":{"ToolCall":{"tool_call_id":"tool-456","fields":{"status":"in_progress"}}}}`),
			},
			expectUpdate: false,
		},
		{
			name: "non-session/update message is ignored",
			inputMsg: &JSONRPCMessage{
				JSONRPC: "2.0",
				Method:  "session/prompt",
				Params:  json.RawMessage(`{"sessionId":"test-session"}`),
			},
			expectUpdate: false,
		},
		{
			name: "AgentMessageChunk is ignored",
			inputMsg: &JSONRPCMessage{
				JSONRPC: "2.0",
				Method:  "session/update",
				Params:  json.RawMessage(`{"sessionId":"test-session","update":{"AgentMessageChunk":{"content":{"type":"text","text":"hello"}}}}`),
			},
			expectUpdate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewProxy()
			p.sessionMux.Lock()
			p.sessionID = "test-session"
			p.sessionMux.Unlock()

			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create pipe: %v", err)
			}
			defer r.Close()

			encoder := json.NewEncoder(w)

			p.handleToolCallNotification(tt.inputMsg, encoder)
			w.Close()

			if !tt.expectUpdate {
				var buf bytes.Buffer
				io.Copy(&buf, r)
				if buf.Len() > 0 {
					t.Errorf("expected no ToolCallUpdate, but got output: %s", buf.String())
				}
				return
			}

			var buf bytes.Buffer
			io.Copy(&buf, r)

			if buf.Len() == 0 {
				t.Fatal("expected ToolCallUpdate notification, got nothing")
			}

			var msg JSONRPCMessage
			if err := json.Unmarshal(buf.Bytes(), &msg); err != nil {
				t.Fatalf("failed to parse ToolCallUpdate: %v", err)
			}

			if msg.Method != "session/update" {
				t.Errorf("expected method session/update, got %q", msg.Method)
			}

			var params struct {
				Update map[string]json.RawMessage `json:"update"`
			}
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("failed to parse params: %v", err)
			}

			updateRaw, ok := params.Update["ToolCallUpdate"]
			if !ok {
				t.Fatal("expected ToolCallUpdate in update")
			}

			var toolCallUpdate struct {
				ToolCallID string `json:"tool_call_id"`
			}
			if err := json.Unmarshal(updateRaw, &toolCallUpdate); err != nil {
				t.Fatalf("failed to parse ToolCallUpdate: %v", err)
			}

			if toolCallUpdate.ToolCallID != tt.expectedToolID {
				t.Errorf("expected tool_call_id %q, got %q", tt.expectedToolID, toolCallUpdate.ToolCallID)
			}
		})
	}
}
