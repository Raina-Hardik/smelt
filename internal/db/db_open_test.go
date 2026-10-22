package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Pragmas must hold for every connection in the pool, not just the first.
func TestOpenAppliesPragmasToEveryConnection(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	ctx := context.Background()
	var conns []interface{ Close() error }
	for i := 0; i < 5; i++ { // held simultaneously, so the pool must open distinct connections
		c, err := d.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		var busy, fk int
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if busy != 5000 || fk != 1 {
			t.Errorf("connection %d: busy_timeout=%d foreign_keys=%d, want 5000 and 1", i, busy, fk)
		}
	}
	for _, c := range conns {
		_ = c.Close()
	}
}

// Concurrent writers - several goroutines, and two handles on the same file as
// when the server and a CLI run share a database - must not lose rows to
// SQLITE_BUSY.
func TestConcurrentWritersDoNotLoseRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	const writers, perWriter = 8, 100
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		h := a
		if w%2 == 1 {
			h = b
		}
		wg.Add(1)
		go func(w int, h *DB) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				err := h.Insert(Record{
					SourcePath: fmt.Sprintf("/m/%d-%d.mkv", w, i), Status: "done", CompletedAt: time.Now(),
				})
				if err != nil {
					errs <- err
				}
			}
		}(w, h)
	}
	wg.Wait()
	close(errs)
	var failed int
	var first error
	for err := range errs {
		failed++
		if first == nil {
			first = err
		}
	}
	if failed > 0 {
		t.Fatalf("%d of %d inserts failed (first: %v)", failed, writers*perWriter, first)
	}
	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM transcodes").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != writers*perWriter {
		t.Errorf("rows = %d, want %d", n, writers*perWriter)
	}
}

// URI syntax characters in the path must not be interpreted by the DSN.
func TestOpenPathWithSpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my data #1 ?x&y=z")
	path := filepath.Join(dir, "his tory.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Insert(Record{SourcePath: "/m/a.mkv", Status: "done", CompletedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("database not created at the requested path: %v", err)
	}
}
