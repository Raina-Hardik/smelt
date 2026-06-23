package worker

import (
	"context"
	"testing"
	"time"
)

func TestGateOpenByDefault(t *testing.T) {
	g := newGate()
	if g.paused() {
		t.Fatal("a new gate should be open, not paused")
	}
	if err := g.wait(context.Background()); err != nil {
		t.Fatalf("wait on an open gate should return nil, got %v", err)
	}
}

func TestGateToggle(t *testing.T) {
	g := newGate()
	if paused := g.toggle(); !paused || !g.paused() {
		t.Fatal("toggle from open should pause")
	}
	if paused := g.toggle(); paused || g.paused() {
		t.Fatal("toggle from paused should resume")
	}
}

func TestGateWaitBlocksWhilePaused(t *testing.T) {
	g := newGate()
	g.toggle() // pause

	done := make(chan struct{})
	go func() {
		_ = g.wait(context.Background())
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("wait returned while gate was paused")
	case <-time.After(30 * time.Millisecond):
		// still blocked, as expected
	}

	g.toggle() // resume
	select {
	case <-done:
		// released, as expected
	case <-time.After(time.Second):
		t.Fatal("wait did not return after resume")
	}
}

func TestGateWaitRespectsContext(t *testing.T) {
	g := newGate()
	g.toggle() // pause
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.wait(ctx); err == nil {
		t.Fatal("wait should return ctx error when cancelled while paused")
	}
}
