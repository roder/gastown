package acp

import (
	"context"
	"testing"
	"time"
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
	if prop.mailIDs == nil {
		t.Error("mailIDs map not initialized")
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
