package main

import (
	"context"

	"github.com/steveyegge/gastown/internal/events"
)

func init() {
	events.StartWatcher(context.Background())
}
