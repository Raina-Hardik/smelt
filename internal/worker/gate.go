package worker

import (
	"context"
	"sync"
)

// gate is a pausable barrier the dispatcher passes through before starting each
// job. While paused, wait blocks; resuming releases all waiters. The zero value
// is not usable — construct with newGate (starts open).
//
// State is carried by ch: a *closed* channel means open (waiters pass through);
// an open channel means paused (waiters block until it is closed on resume).
type gate struct {
	mu sync.Mutex
	ch chan struct{}
}

func newGate() *gate {
	ch := make(chan struct{})
	close(ch) // start open
	return &gate{ch: ch}
}

// wait blocks while the gate is paused, returning ctx.Err() if ctx is cancelled
// first. It returns immediately when the gate is open.
func (g *gate) wait(ctx context.Context) error {
	g.mu.Lock()
	ch := g.ch
	g.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// toggle flips the gate and reports the new paused state.
func (g *gate) toggle() (paused bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.ch: // open → pause
		g.ch = make(chan struct{})
		return true
	default: // paused → resume
		close(g.ch)
		return false
	}
}

func (g *gate) paused() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.ch:
		return false
	default:
		return true
	}
}
