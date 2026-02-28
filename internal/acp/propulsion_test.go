package acp

import (
	"context"
	"sync"
	"testing"
	"time"

	beadsdk "github.com/steveyegge/beads"
)

func TestNewPropeller(t *testing.T) {
	proxy := NewProxy()
	prop := NewPropeller(proxy, nil)

	if prop.proxy != proxy {
		t.Error("proxy not set correctly")
	}
	if prop.lastSeen == nil {
		t.Error("lastSeen map not initialized")
	}
}

func TestPropeller_StateTracking(t *testing.T) {
	prop := NewPropeller(nil, nil)

	key := "bead:gt-123"

	changed, isNew := prop.hasStateChanged(key, "open")
	if !isNew {
		t.Error("first state should be new")
	}
	if changed {
		t.Error("first state should not be changed (it's new)")
	}

	prop.setLastState(key, "open")

	changed, isNew = prop.hasStateChanged(key, "open")
	if isNew {
		t.Error("existing state should not be new")
	}
	if changed {
		t.Error("same state should not change")
	}

	changed, isNew = prop.hasStateChanged(key, "in_progress")
	if isNew {
		t.Error("existing state should not be new")
	}
	if !changed {
		t.Error("different state should change")
	}

	prop.setLastState(key, "in_progress")

	if prop.getLastState(key) != "in_progress" {
		t.Error("getLastState returned wrong value")
	}
}

func TestPropeller_FormatNotification(t *testing.T) {
	tests := []struct {
		name     string
		change   stateChange
		expected string
	}{
		{
			name: "bead completed",
			change: stateChange{
				entityType: "bead",
				entityID:   "gt-123",
				entityName: "Test task",
				newState:   "closed",
			},
			expected: "✅ Bead 'gt-123' (Test task) completed.",
		},
		{
			name: "bead started",
			change: stateChange{
				entityType: "bead",
				entityID:   "gt-456",
				entityName: "",
				newState:   "in_progress",
			},
			expected: "⏳ Bead 'gt-456' started work.",
		},
		{
			name: "convoy completed",
			change: stateChange{
				entityType: "convoy",
				entityID:   "hq-abc",
				entityName: "Deploy feature",
				newState:   "completed",
			},
			expected: "✅ Convoy 'hq-abc' (Deploy feature) completed.",
		},
		{
			name: "convoy failed",
			change: stateChange{
				entityType: "convoy",
				entityID:   "hq-def",
				entityName: "",
				newState:   "failed",
			},
			expected: "❌ Convoy 'hq-def' failed.",
		},
		{
			name: "polecat completed",
			change: stateChange{
				entityType: "polecat",
				entityID:   "gastown/polecats/Toast",
				entityName: "gt-toast-polecat",
				newState:   "closed",
			},
			expected: "✅ Polecat 'gastown/polecats/Toast' completed task.",
		},
		{
			name: "polecat failed",
			change: stateChange{
				entityType: "polecat",
				entityID:   "gastown/polecats/Toast",
				newState:   "failed",
			},
			expected: "❌ Polecat 'gastown/polecats/Toast' failed.",
		},
		{
			name: "unknown state",
			change: stateChange{
				entityType: "bead",
				entityID:   "gt-999",
				newState:   "unknown",
			},
			expected: "",
		},
	}

	prop := NewPropeller(nil, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prop.formatNotification(tt.change)
			if got != tt.expected {
				t.Errorf("formatNotification() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestPropeller_StartStop(t *testing.T) {
	prop := NewPropeller(nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	prop.Start(ctx)

	time.Sleep(100 * time.Millisecond)

	prop.Stop()
}

func TestPropeller_DetectChangesWithNilStore(t *testing.T) {
	prop := NewPropeller(nil, nil)

	ctx := context.Background()
	changes := prop.detectChanges(ctx)

	if len(changes) != 0 {
		t.Errorf("detectChanges with nil store should return empty, got %d", len(changes))
	}
}

type mockStorage struct {
	beadsdk.Storage
	issues []*beadsdk.Issue
	mu     sync.Mutex
}

func (m *mockStorage) SearchIssues(ctx context.Context, query string, filter beadsdk.IssueFilter) ([]*beadsdk.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.issues, nil
}

func TestPropeller_DetectBeadChanges(t *testing.T) {
	mock := &mockStorage{
		issues: []*beadsdk.Issue{
			{ID: "gt-123", Status: beadsdk.StatusOpen, Title: "Test task"},
		},
	}

	prop := NewPropeller(nil, mock)
	ctx := context.Background()

	changes := prop.detectBeadChanges(ctx)

	if len(changes) != 0 {
		t.Errorf("first poll should not produce changes, got %d", len(changes))
	}

	mock.mu.Lock()
	mock.issues[0].Status = beadsdk.StatusInProgress
	mock.mu.Unlock()

	changes = prop.detectBeadChanges(ctx)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	if changes[0].oldState != "open" {
		t.Errorf("expected oldState 'open', got %q", changes[0].oldState)
	}
	if changes[0].newState != "in_progress" {
		t.Errorf("expected newState 'in_progress', got %q", changes[0].newState)
	}
}

func TestPropeller_DetectConvoyChanges(t *testing.T) {
	convoyLabel := "gt:convoy"
	mock := &mockStorage{
		issues: []*beadsdk.Issue{
			{ID: "hq-abc", Status: beadsdk.StatusOpen, Title: "Deploy feature", Labels: []string{convoyLabel}},
		},
	}

	prop := NewPropeller(nil, mock)
	ctx := context.Background()

	changes := prop.detectConvoyChanges(ctx)

	if len(changes) != 0 {
		t.Errorf("first poll should not produce changes, got %d", len(changes))
	}

	mock.mu.Lock()
	mock.issues[0].Status = beadsdk.StatusClosed
	mock.mu.Unlock()

	changes = prop.detectConvoyChanges(ctx)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	if changes[0].entityType != "convoy" {
		t.Errorf("expected entityType 'convoy', got %q", changes[0].entityType)
	}
}

func TestPropeller_DetectPolecatChanges(t *testing.T) {
	agentLabel := "gt:agent"
	mock := &mockStorage{
		issues: []*beadsdk.Issue{
			{ID: "gt-toast-agent", Status: beadsdk.StatusOpen, Assignee: "gastown/polecats/Toast", Labels: []string{agentLabel}},
		},
	}

	prop := NewPropeller(nil, mock)
	ctx := context.Background()

	changes := prop.detectPolecatChanges(ctx)

	if len(changes) != 0 {
		t.Errorf("first poll should not produce changes, got %d", len(changes))
	}

	mock.mu.Lock()
	mock.issues[0].Status = beadsdk.StatusInProgress
	mock.mu.Unlock()

	changes = prop.detectPolecatChanges(ctx)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	if changes[0].entityType != "polecat" {
		t.Errorf("expected entityType 'polecat', got %q", changes[0].entityType)
	}
	if changes[0].entityID != "gastown/polecats/Toast" {
		t.Errorf("expected entityID 'gastown/polecats/Toast', got %q", changes[0].entityID)
	}
}

func TestPropeller_NotifyChange(t *testing.T) {
	prop := NewPropeller(nil, nil)

	change := stateChange{
		entityType: "bead",
		entityID:   "gt-123",
		entityName: "Test task",
		newState:   "closed",
	}

	prop.notifyChange(change)
}

func TestPropeller_ConcurrentAccess(t *testing.T) {
	prop := NewPropeller(nil, nil)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			key := "bead:" + string(rune('a'+i%26))
			prop.setLastState(key, "open")
		}(i)
		go func(i int) {
			defer wg.Done()
			key := "bead:" + string(rune('a'+i%26))
			_ = prop.getLastState(key)
		}(i)
	}
	wg.Wait()
}
