package events

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	beadsdk "github.com/steveyegge/beads"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	sentNotifications = make(map[string]bool)
	mu                sync.Mutex
)

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
		return
	}

	doltPath := filepath.Join(townRoot, ".beads", "dolt")
	store, err := beadsdk.Open(ctx, doltPath)
	if err != nil {
		return
	}
	defer store.Close()

	// TODO: Implement convoy tracking and notification.
	// This requires resolving the import cycle with the mail package.
	_ = store
}
