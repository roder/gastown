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
