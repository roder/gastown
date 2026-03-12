package acp

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/nudge"
)

func TestNewPropeller(t *testing.T) {
	proxy := NewProxy()
	prop := NewPropeller(proxy, "/town", "hq-mayor")

	if prop.proxy != proxy {
		t.Error("proxy not set correctly")
	}
	if prop.townRoot != "/town" {
		t.Error("townRoot not set correctly")
	}
	if prop.session != "hq-mayor" {
		t.Error("session not set correctly")
	}
}

func TestPropeller_StartStop(t *testing.T) {
	prop := NewPropeller(nil, "", "hq-mayor")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	prop.Start(ctx)

	time.Sleep(100 * time.Millisecond)

	prop.Stop()
}

func TestPropeller_DeliverNudges_NoProxy(t *testing.T) {
	// Test that deliverNudges handles nil proxy gracefully
	prop := NewPropeller(nil, "/town", "hq-mayor")
	prop.deliverNudges() // Should not panic
}

func TestPropeller_EventLoop_Cancellation(t *testing.T) {
	// Test that eventLoop exits on context cancellation
	prop := NewPropeller(nil, "/town", "hq-mayor")

	ctx, cancel := context.WithCancel(context.Background())
	prop.ctx = ctx
	prop.cancel = cancel

	// Start eventLoop in a goroutine
	done := make(chan struct{})
	go func() {
		prop.eventLoop()
		close(done)
	}()

	// Cancel context
	cancel()

	// Wait for eventLoop to exit
	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Error("eventLoop did not exit after context cancellation")
	}
}

func TestPropeller_DeliverNudges_RequeuesWhenSessionUnavailable(t *testing.T) {
	townRoot := t.TempDir()
	proxy := NewProxy()
	prop := NewPropeller(proxy, townRoot, "hq-mayor")

	if err := nudge.Enqueue(townRoot, "hq-mayor", nudge.QueuedNudge{
		Sender:   "witness",
		Message:  "Escalation pending",
		Priority: nudge.PriorityUrgent,
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	prop.deliverNudges()

	pending, err := nudge.Pending(townRoot, "hq-mayor")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending != 1 {
		t.Fatalf("expected requeued nudge to remain pending, got %d", pending)
	}

	drained, err := nudge.Drain(townRoot, "hq-mayor")
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(drained) != 1 {
		t.Fatalf("expected 1 requeued nudge, got %d", len(drained))
	}
	if drained[0].Priority != nudge.PriorityUrgent {
		t.Fatalf("priority = %q, want %q", drained[0].Priority, nudge.PriorityUrgent)
	}
}

func TestPropeller_NotifyReturnsErrorWithoutSessionID(t *testing.T) {
	proxy := NewProxy()
	prop := NewPropeller(proxy, t.TempDir(), "hq-mayor")

	err := prop.notify("test message", map[string]string{"gt/eventType": "nudge"}, true)
	if err == nil {
		t.Fatal("expected notify to fail when sessionID is unavailable")
	}
}
