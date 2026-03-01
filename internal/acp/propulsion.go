package acp

import (
	"context"
	"fmt"
	"sync"
	"time"

	beadsdk "github.com/steveyegge/beads"
)

const pollInterval = 30 * time.Second

type stateChange struct {
	entityType string
	entityID   string
	entityName string
	oldState   string
	newState   string
}

type Propeller struct {
	proxy      *Proxy
	store      beadsdk.Storage
	lastSeen   map[string]string
	lastSeenMu sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

func NewPropeller(proxy *Proxy, store beadsdk.Storage) *Propeller {
	return &Propeller{
		proxy:    proxy,
		store:    store,
		lastSeen: make(map[string]string),
	}
}

func (p *Propeller) Start(ctx context.Context) {
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.wg.Add(1)
	go p.pollLoop()
}

func (p *Propeller) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
}

func (p *Propeller) pollLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	p.pollOnce()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce()
		}
	}
}

func (p *Propeller) pollOnce() {
	ctx := p.ctx

	changes := p.detectChanges(ctx)
	for _, change := range changes {
		p.notifyChange(change)
	}
}

func (p *Propeller) detectChanges(ctx context.Context) []stateChange {
	var changes []stateChange

	if p.store == nil {
		return changes
	}

	// Single query for all open issues instead of 3 separate queries
	statusOpen := beadsdk.StatusOpen
	issues, err := p.store.SearchIssues(ctx, "", beadsdk.IssueFilter{Status: &statusOpen})
	if err != nil {
		return changes
	}

	// Process all issues in memory, categorizing by labels
	for _, issue := range issues {
		// Check for convoy label
		if hasLabel(issue.Labels, "gt:convoy") {
			if change := p.checkStateChange("convoy", issue.ID, issue.Title, string(issue.Status)); change != nil {
				changes = append(changes, *change)
			}
		}

		// Check for agent label (polecat)
		if hasLabel(issue.Labels, "gt:agent") && issue.Assignee != "" {
			if change := p.checkStateChange("polecat", issue.Assignee, issue.ID, string(issue.Status)); change != nil {
				changes = append(changes, *change)
			}
		}

		// All open issues are beads (unless they have special labels)
		if !hasLabel(issue.Labels, "gt:convoy") && !hasLabel(issue.Labels, "gt:agent") {
			if change := p.checkStateChange("bead", issue.ID, issue.Title, string(issue.Status)); change != nil {
				changes = append(changes, *change)
			}
		}
	}

	return changes
}

// hasLabel checks if a label exists in the label slice
func hasLabel(labels []string, target string) bool {
	for _, label := range labels {
		if label == target {
			return true
		}
	}
	return false
}

// checkStateChange checks if state changed and returns a stateChange if so
func (p *Propeller) checkStateChange(entityType, entityID, entityName, currentState string) *stateChange {
	key := entityType + ":" + entityID

	changed, isNew := p.hasStateChanged(key, currentState)
	if isNew {
		p.setLastState(key, currentState)
		return nil
	}
	if changed {
		oldState := p.getLastState(key)
		p.setLastState(key, currentState)
		return &stateChange{
			entityType: entityType,
			entityID:   entityID,
			entityName: entityName,
			oldState:   oldState,
			newState:   currentState,
		}
	}
	return nil
}

func (p *Propeller) hasStateChanged(key, currentState string) (changed bool, isNew bool) {
	p.lastSeenMu.RLock()
	defer p.lastSeenMu.RUnlock()

	lastState, exists := p.lastSeen[key]
	if !exists {
		return false, true
	}
	return lastState != currentState, false
}

func (p *Propeller) getLastState(key string) string {
	p.lastSeenMu.RLock()
	defer p.lastSeenMu.RUnlock()
	return p.lastSeen[key]
}

func (p *Propeller) setLastState(key, state string) {
	p.lastSeenMu.Lock()
	defer p.lastSeenMu.Unlock()
	p.lastSeen[key] = state
}

func (p *Propeller) notifyChange(change stateChange) {
	if p.proxy == nil {
		return
	}

	text := p.formatNotification(change)
	if text == "" {
		return
	}

	params := map[string]any{
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{
				"type": "text",
				"text": text,
			},
		},
	}

	_ = p.proxy.InjectNotification("session/update", params)
}

func (p *Propeller) formatNotification(change stateChange) string {
	if change.newState == "closed" || change.newState == "completed" {
		return p.formatSuccessNotification(change)
	}

	if change.newState == "failed" {
		return p.formatFailureNotification(change)
	}

	if change.newState == "in_progress" {
		return p.formatProgressNotification(change)
	}

	return ""
}

func (p *Propeller) formatSuccessNotification(change stateChange) string {
	switch change.entityType {
	case "bead":
		if change.entityName != "" {
			return fmt.Sprintf("✅ Bead '%s' (%s) completed.", change.entityID, change.entityName)
		}
		return fmt.Sprintf("✅ Bead '%s' completed.", change.entityID)
	case "convoy":
		if change.entityName != "" {
			return fmt.Sprintf("✅ Convoy '%s' (%s) completed.", change.entityID, change.entityName)
		}
		return fmt.Sprintf("✅ Convoy '%s' completed.", change.entityID)
	case "polecat":
		return fmt.Sprintf("✅ Polecat '%s' completed task.", change.entityID)
	}
	return ""
}

func (p *Propeller) formatFailureNotification(change stateChange) string {
	switch change.entityType {
	case "bead":
		return fmt.Sprintf("❌ Bead '%s' failed.", change.entityID)
	case "convoy":
		return fmt.Sprintf("❌ Convoy '%s' failed.", change.entityID)
	case "polecat":
		return fmt.Sprintf("❌ Polecat '%s' failed.", change.entityID)
	}
	return ""
}

func (p *Propeller) formatProgressNotification(change stateChange) string {
	switch change.entityType {
	case "bead":
		if change.entityName != "" {
			return fmt.Sprintf("⏳ Bead '%s' (%s) started work.", change.entityID, change.entityName)
		}
		return fmt.Sprintf("⏳ Bead '%s' started work.", change.entityID)
	case "convoy":
		if change.entityName != "" {
			return fmt.Sprintf("⏳ Convoy '%s' (%s) in progress.", change.entityID, change.entityName)
		}
		return fmt.Sprintf("⏳ Convoy '%s' in progress.", change.entityID)
	case "polecat":
		return fmt.Sprintf("⏳ Polecat '%s' started work.", change.entityID)
	}
	return ""
}
