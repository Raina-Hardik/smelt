package server

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/Raina-Hardik/smelt/internal/db"
)

// TestStart_ShutsDownOnContextCancel guards the SIGINT/SIGTERM handling path:
// cmd.Execute wires a signal-aware context (signal.NotifyContext) through to
// Start, and Start must actually observe it and return instead of blocking
// forever in http.ListenAndServe.
func TestStart_ShutsDownOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	srv := New(database, dir, "smelt", "")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx, "127.0.0.1:0") }()

	// Give the listener goroutine a moment to actually start serving.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return within shutdownTimeout after context cancellation")
	}
}

func TestStart_ReturnsListenError(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// Occupy a port, then try to bind the server to the exact same address.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	addr := ln.Addr().String()

	srv := New(database, dir, "smelt", "")
	err = srv.Start(context.Background(), addr)
	if err == nil {
		t.Fatal("expected an error binding to an already-in-use address")
	}
}
