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

	"github.com/steveyegge/gastown/internal/nudge"
)

const pollInterval = 30 * time.Second

type mailMessage struct {
	ID       string
	Subject  string
	From     string
	Read     bool
	Escalate bool
}

type Propeller struct {
	proxy     *Proxy
	townRoot  string
	session   string
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mailIDs   map[string]mailMessage
	mailMu    sync.RWMutex
	hookState string
	hookMu    sync.RWMutex
}

func NewPropeller(proxy *Proxy, townRoot, session string) *Propeller {
	return &Propeller{
		proxy:    proxy,
		townRoot: townRoot,
		session:  session,
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

	p.detectMailChanges(ctx)
	p.detectHookChanges(ctx)
	p.detectNudges(ctx)
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

func (p *Propeller) detectNudges(ctx context.Context) {
	if p.townRoot == "" || p.session == "" {
		return
	}

	nudges, err := nudge.Drain(p.townRoot, p.session)
	if err != nil || len(nudges) == 0 {
		return
	}

	var urgent, normal []nudge.QueuedNudge
	for _, n := range nudges {
		if n.Priority == nudge.PriorityUrgent {
			urgent = append(urgent, n)
		} else {
			normal = append(normal, n)
		}
	}

	var text string
	if len(urgent) > 0 {
		text = fmt.Sprintf("🚨 NUDGE (%d urgent): ", len(urgent))
		for i, n := range urgent {
			if i > 0 {
				text += " | "
			}
			text += fmt.Sprintf("[%s] %s", n.Sender, n.Message)
		}
		if len(normal) > 0 {
			text += fmt.Sprintf(" (+%d other)", len(normal))
		}
	} else {
		text = fmt.Sprintf("📨 NUDGE from %s: %s", nudges[0].Sender, nudges[0].Message)
		if len(nudges) > 1 {
			text += fmt.Sprintf(" (+%d more)", len(nudges)-1)
		}
	}

	meta := map[string]string{
		"gt/eventType": "nudge",
		"gt/count":     strconv.Itoa(len(nudges)),
		"gt/urgent":    strconv.Itoa(len(urgent)),
		"gt/drained":   "true",
		"gt/session":   p.session,
	}

	p.notifyWithMeta(text, meta)
}
