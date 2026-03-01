package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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
	meta       map[string]string
}

type mailMessage struct {
	ID       string
	Subject  string
	From     string
	Read     bool
	Escalate bool
}

type Propeller struct {
	proxy      *Proxy
	store      beadsdk.Storage
	lastSeen   map[string]string
	lastSeenMu sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mailIDs    map[string]mailMessage
	mailMu     sync.RWMutex
	hookState  string
	hookMu     sync.RWMutex
}

func NewPropeller(proxy *Proxy, store beadsdk.Storage) *Propeller {
	return &Propeller{
		proxy:    proxy,
		store:    store,
		lastSeen: make(map[string]string),
		mailIDs:  make(map[string]mailMessage),
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

	p.detectMailChanges(ctx)
	p.detectHookChanges(ctx)
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

	updateMap := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content": map[string]any{
			"type": "text",
			"text": text,
		},
	}

	if change.meta != nil {
		updateMap["_meta"] = change.meta
	}

	params := map[string]any{
		"update": updateMap,
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

func (p *Propeller) detectMailChanges(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "gt", "mail", "inbox", "--identity", "mayor/", "--json")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	var mailResp struct {
		Messages []struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
			From    string `json:"from"`
			Read    bool   `json:"read"`
		} `json:"messages"`
	}

	if err := json.Unmarshal(output, &mailResp); err != nil {
		return
	}

	p.mailMu.Lock()
	defer p.mailMu.Unlock()

	newMailIDs := make(map[string]mailMessage)
	unreadCount := 0
	escalationCount := 0

	for _, msg := range mailResp.Messages {
		newMailIDs[msg.ID] = mailMessage{
			ID:      msg.ID,
			Subject: msg.Subject,
			From:    msg.From,
			Read:    msg.Read,
		}

		isNew := false
		if _, exists := p.mailIDs[msg.ID]; !exists {
			isNew = true
		}

		if isNew || !msg.Read {
			unreadCount++
		}

		isEscalation := strings.Contains(strings.ToLower(msg.Subject), "escalation") ||
			strings.Contains(strings.ToLower(msg.Subject), "help") ||
			strings.Contains(strings.ToLower(msg.Subject), "urgent")
		if isEscalation && (isNew || !msg.Read) {
			escalationCount++
		}
	}

	hasNewMail := len(newMailIDs) != len(p.mailIDs)
	if hasNewMail && unreadCount > 0 {
		p.notifyMailEvent(unreadCount, escalationCount)
	}

	p.mailIDs = newMailIDs
}

func (p *Propeller) notifyMailEvent(count, escalationCount int) {
	if p.proxy == nil {
		return
	}

	meta := map[string]string{
		"gt/eventType": "mail",
		"gt/count":     strconv.Itoa(count),
	}
	if escalationCount > 0 {
		meta["gt/escalationCount"] = strconv.Itoa(escalationCount)
	}

	p.notifyWithMeta("📬 You have new mail. Run 'gt mail inbox --identity mayor/' to read.", meta)
}

func (p *Propeller) detectHookChanges(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "gt", "hook", "show", "mayor", "--json")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	var hookResp struct {
		HasWork  bool   `json:"hasWork"`
		Title    string `json:"title"`
		Molecule string `json:"molecule"`
	}

	if err := json.Unmarshal(output, &hookResp); err != nil {
		return
	}

	newState := "idle"
	if hookResp.HasWork {
		newState = "working"
	}

	p.hookMu.Lock()
	oldState := p.hookState
	if oldState != newState {
		p.hookState = newState
		p.hookMu.Unlock()
		p.notifyHookChange(oldState, newState, hookResp.Title)
	} else {
		p.hookMu.Unlock()
	}
}

func (p *Propeller) notifyHookChange(oldState, newState, title string) {
	if p.proxy == nil {
		return
	}

	meta := map[string]string{
		"gt/eventType": "hook",
		"gt/oldState":  oldState,
		"gt/newState":  newState,
	}

	text := "⚓ Hook status changed"
	if newState == "working" && title != "" {
		text = fmt.Sprintf("⚓ Work hooked to mayor: %s", title)
	} else if newState == "idle" {
		text = "⚓ Mayor hook cleared"
	}

	p.notifyWithMeta(text, meta)
}

func (p *Propeller) notifyWithMeta(text string, meta map[string]string) {
	if p.proxy == nil || text == "" {
		return
	}

	params := map[string]any{
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{
				"type": "text",
				"text": text,
			},
			"_meta": meta,
		},
	}

	_ = p.proxy.InjectNotification("session/update", params)
}
