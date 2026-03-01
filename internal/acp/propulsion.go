package acp

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	beadsdk "github.com/steveyegge/beads"
)

const pollInterval = 3 * time.Second

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

	changes = append(changes, p.detectBeadChanges(ctx)...)
	changes = append(changes, p.detectConvoyChanges(ctx)...)
	changes = append(changes, p.detectPolecatChanges(ctx)...)

	return changes
}

func (p *Propeller) detectBeadChanges(ctx context.Context) []stateChange {
	var changes []stateChange

	if p.store == nil {
		return changes
	}

	statusOpen := beadsdk.StatusOpen
	issues, err := p.store.SearchIssues(ctx, "", beadsdk.IssueFilter{Status: &statusOpen})
	if err != nil {
		return changes
	}

	for _, issue := range issues {
		key := "bead:" + issue.ID
		currentState := string(issue.Status)

		changed, isNew := p.hasStateChanged(key, currentState)
		if isNew {
			p.setLastState(key, currentState)
			continue
		}
		if changed {
			oldState := p.getLastState(key)
			p.setLastState(key, currentState)
			changes = append(changes, stateChange{
				entityType: "bead",
				entityID:   issue.ID,
				entityName: issue.Title,
				oldState:   oldState,
				newState:   currentState,
			})
		}
	}

	return changes
}

func (p *Propeller) detectConvoyChanges(ctx context.Context) []stateChange {
	var changes []stateChange

	if p.store == nil {
		return changes
	}

	statusOpen := beadsdk.StatusOpen
	convoyLabel := "gt:convoy"
	issues, err := p.store.SearchIssues(ctx, "", beadsdk.IssueFilter{
		Status: &statusOpen,
		Labels: []string{convoyLabel},
	})
	if err != nil {
		return changes
	}

	for _, issue := range issues {
		key := "convoy:" + issue.ID
		currentState := string(issue.Status)

		changed, isNew := p.hasStateChanged(key, currentState)
		if isNew {
			p.setLastState(key, currentState)
			continue
		}
		if changed {
			oldState := p.getLastState(key)
			p.setLastState(key, currentState)
			changes = append(changes, stateChange{
				entityType: "convoy",
				entityID:   issue.ID,
				entityName: issue.Title,
				oldState:   oldState,
				newState:   currentState,
			})
		}
	}

	return changes
}

func (p *Propeller) detectPolecatChanges(ctx context.Context) []stateChange {
	var changes []stateChange

	if p.store == nil {
		return changes
	}

	statusOpen := beadsdk.StatusOpen
	agentLabel := "gt:agent"
	issues, err := p.store.SearchIssues(ctx, "", beadsdk.IssueFilter{
		Status: &statusOpen,
		Labels: []string{agentLabel},
	})
	if err != nil {
		return changes
	}

	for _, issue := range issues {
		if issue.Assignee == "" {
			continue
		}

		key := "polecat:" + issue.Assignee
		currentState := string(issue.Status)

		changed, isNew := p.hasStateChanged(key, currentState)
		if isNew {
			p.setLastState(key, currentState)
			continue
		}
		if changed {
			oldState := p.getLastState(key)
			p.setLastState(key, currentState)
			changes = append(changes, stateChange{
				entityType: "polecat",
				entityID:   issue.Assignee,
				entityName: issue.ID,
				oldState:   oldState,
				newState:   currentState,
			})
		}
	}

	return changes
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
		log.Printf("warning: ACP Propeller cannot notify: proxy is nil")
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

	if err := p.proxy.InjectNotification("session/update", params); err != nil {
		log.Printf("warning: ACP Propeller failed to inject notification for %s %s: %v",
			change.entityType, change.entityID, err)
	}
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
