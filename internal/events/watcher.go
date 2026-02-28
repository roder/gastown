// Package events provides event logging for the gt activity feed.
package events

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	beadsdk "github.comcom/steveyegge/beads"
	"github.com/steveyegge/gastown/internal/convoy"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	sentNotifications = make(map[string]bool)
	mu                sync.Mutex
)

// StartWatcher starts a new goroutine to watch for convoy and bead events.
func StartWatcher(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollAndNotify(ctx)
			}
		}
	}()
}

func pollAndNotify(ctx context.Context) {
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		// Not in a workspace, do nothing.
		return
	}

	doltPath := filepath.Join(townRoot, ".beads", "dolt")
	store, err := beadsdk.Open(ctx, doltPath)
	if err != nil {
		fmt.Printf("Error opening beads store: %v\n", err)
		return
	}
	defer store.Close()

	// Get all open convoys.
	query := `SELECT * FROM issues WHERE type = 'convoy' AND status = 'open'`
	results, err := store.Search(ctx, query)
	if err != nil {
		fmt.Printf("Error searching for convoys: %v\n", err)
		return
	}

	for _, result := range results {
		convoyID := result.ID
		trackedIssues := convoy.GetConvoyTrackedIssues(ctx, store, convoyID, townRoot)
		for _, issue := range trackedIssues {
			if issue.Status == "closed" {
				mu.Lock()
				if !sentNotifications[issue.ID] {
					sendCompletionNotification(townRoot, convoyID, issue.ID)
					sentNotifications[issue.ID] = true
				}
				mu.Unlock()
			}
		}
	}
}

func sendCompletionNotification(townRoot, convoyID, issueID string) {
	router := mail.NewRouter(townRoot)
	defer router.WaitPendingNotifications()

	msg := &mail.Message{
		To:      "mayor",
		From:    "events-watcher",
		Subject: fmt.Sprintf("Issue %s in convoy %s completed", issueID, convoyID),
		Body:    fmt.Sprintf("Issue %s in convoy %s has been completed.", issueID, convoyID),
	}

	if err := router.Send(msg); err != nil {
		fmt.Printf("Error sending notification: %v\n", err)
	}
}
