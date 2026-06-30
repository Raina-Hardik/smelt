package db

import (
	"testing"
)

// openMemory is defined in db_runs_test.go (same package).

func TestCreateAndGetProgram(t *testing.T) {
	d := openMemory(t)

	if err := d.CreateProgram("p1", "nightly", "0 3 * * *", "/mnt/media", "mkv,mp4", `[]`); err != nil {
		t.Fatalf("CreateProgram: %v", err)
	}

	rec, err := d.GetProgram("p1")
	if err != nil {
		t.Fatalf("GetProgram: %v", err)
	}
	if rec == nil {
		t.Fatal("expected record, got nil")
	}
	if rec.ID != "p1" {
		t.Errorf("ID = %q, want p1", rec.ID)
	}
	if rec.Name != "nightly" {
		t.Errorf("Name = %q, want nightly", rec.Name)
	}
	if rec.Schedule != "0 3 * * *" {
		t.Errorf("Schedule = %q", rec.Schedule)
	}
	if rec.Src != "/mnt/media" {
		t.Errorf("Src = %q", rec.Src)
	}
	if rec.Ext != "mkv,mp4" {
		t.Errorf("Ext = %q", rec.Ext)
	}
	if rec.CreatedAt.IsZero() || rec.UpdatedAt.IsZero() {
		t.Error("timestamps should be non-zero")
	}
}

func TestGetProgram_NotFound(t *testing.T) {
	d := openMemory(t)
	rec, err := d.GetProgram("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec != nil {
		t.Errorf("expected nil, got %+v", rec)
	}
}

func TestListPrograms_Empty(t *testing.T) {
	d := openMemory(t)
	recs, err := d.ListPrograms()
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected empty list, got %d", len(recs))
	}
}

func TestListPrograms_OrderedByName(t *testing.T) {
	d := openMemory(t)

	for _, args := range [][]string{
		{"c", "Zeta", "", "/c", "mkv", `[]`},
		{"a", "Alpha", "", "/a", "mkv", `[]`},
		{"b", "Beta", "", "/b", "mkv", `[]`},
	} {
		if err := d.CreateProgram(args[0], args[1], args[2], args[3], args[4], args[5]); err != nil {
			t.Fatalf("CreateProgram %s: %v", args[0], err)
		}
	}

	recs, err := d.ListPrograms()
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}
	if recs[0].Name != "Alpha" || recs[1].Name != "Beta" || recs[2].Name != "Zeta" {
		t.Errorf("not sorted by name: %v %v %v", recs[0].Name, recs[1].Name, recs[2].Name)
	}
}

func TestUpdateProgram(t *testing.T) {
	d := openMemory(t)

	if err := d.CreateProgram("p1", "old-name", "", "/old", "mkv", `[]`); err != nil {
		t.Fatalf("CreateProgram: %v", err)
	}
	origRec, _ := d.GetProgram("p1")

	if err := d.UpdateProgram("p1", "new-name", "0 4 * * *", "/new", "mp4,avi", `[{"do":{"cmd":"skip"}}]`); err != nil {
		t.Fatalf("UpdateProgram: %v", err)
	}

	rec, err := d.GetProgram("p1")
	if err != nil {
		t.Fatalf("GetProgram after update: %v", err)
	}
	if rec.Name != "new-name" {
		t.Errorf("Name = %q, want new-name", rec.Name)
	}
	if rec.Schedule != "0 4 * * *" {
		t.Errorf("Schedule = %q", rec.Schedule)
	}
	if rec.Src != "/new" {
		t.Errorf("Src = %q", rec.Src)
	}
	if rec.Ext != "mp4,avi" {
		t.Errorf("Ext = %q", rec.Ext)
	}
	if !rec.UpdatedAt.After(origRec.UpdatedAt) && !rec.UpdatedAt.Equal(origRec.UpdatedAt) {
		t.Error("UpdatedAt should be >= original")
	}
	if rec.CreatedAt != origRec.CreatedAt {
		t.Error("CreatedAt should not change on update")
	}
}

func TestDeleteProgram(t *testing.T) {
	d := openMemory(t)

	if err := d.CreateProgram("p1", "to-delete", "", "/x", "mkv", `[]`); err != nil {
		t.Fatalf("CreateProgram: %v", err)
	}

	if err := d.DeleteProgram("p1"); err != nil {
		t.Fatalf("DeleteProgram: %v", err)
	}

	rec, err := d.GetProgram("p1")
	if err != nil {
		t.Fatalf("GetProgram after delete: %v", err)
	}
	if rec != nil {
		t.Error("expected nil after delete, got record")
	}
}

func TestDeleteProgram_Nonexistent(t *testing.T) {
	d := openMemory(t)
	// Deleting a non-existent program should not error.
	if err := d.DeleteProgram("ghost"); err != nil {
		t.Errorf("DeleteProgram nonexistent: want nil, got %v", err)
	}
}

func TestCreateProgram_DuplicateID(t *testing.T) {
	d := openMemory(t)
	if err := d.CreateProgram("dup", "first", "", "/a", "mkv", `[]`); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := d.CreateProgram("dup", "second", "", "/b", "mkv", `[]`); err == nil {
		t.Error("expected error on duplicate ID, got nil")
	}
}

func TestRecentRuns_All(t *testing.T) {
	d := openMemory(t)

	for _, args := range [][]string{{"r1", "alpha"}, {"r2", "beta"}, {"r3", "gamma"}} {
		if err := d.StartRun(args[0], args[1], 1); err != nil {
			t.Fatalf("StartRun %s: %v", args[0], err)
		}
	}
	_ = d.FinishRun("r1", 1, 0, 0)

	recs, err := d.RecentRuns(50, "")
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(recs))
	}
}

func TestRecentRuns_StatusFilter(t *testing.T) {
	d := openMemory(t)

	_ = d.StartRun("r-running", "running", 1)
	_ = d.StartRun("r-done", "done", 1)
	_ = d.FinishRun("r-done", 1, 0, 0)
	_ = d.StartRun("r-failed", "failed", 1)
	_ = d.FinishRun("r-failed", 0, 1, 0)

	running, err := d.RecentRuns(50, "running")
	if err != nil {
		t.Fatalf("RecentRuns running: %v", err)
	}
	if len(running) != 1 || running[0].RunID != "r-running" {
		t.Errorf("running filter: got %v", running)
	}

	done, err := d.RecentRuns(50, "done")
	if err != nil {
		t.Fatalf("RecentRuns done: %v", err)
	}
	if len(done) != 1 || done[0].RunID != "r-done" {
		t.Errorf("done filter: got %v", done)
	}

	failed, err := d.RecentRuns(50, "failed")
	if err != nil {
		t.Fatalf("RecentRuns failed: %v", err)
	}
	if len(failed) != 1 || failed[0].RunID != "r-failed" {
		t.Errorf("failed filter: got %v", failed)
	}
}

func TestRecentRuns_Limit(t *testing.T) {
	d := openMemory(t)

	for i := range 10 {
		id := "r" + string(rune('0'+i))
		if err := d.StartRun(id, "", 1); err != nil {
			t.Fatalf("StartRun: %v", err)
		}
	}

	recs, err := d.RecentRuns(3, "")
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(recs) != 3 {
		t.Errorf("limit=3: expected 3, got %d", len(recs))
	}
}

func TestGetRun_Found(t *testing.T) {
	d := openMemory(t)

	if err := d.StartRun("rget", "myrun", 5); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	rec, err := d.GetRun("rget")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if rec == nil {
		t.Fatal("expected record, got nil")
	}
	if rec.RunID != "rget" {
		t.Errorf("RunID = %q", rec.RunID)
	}
	if rec.Name != "myrun" {
		t.Errorf("Name = %q", rec.Name)
	}
	if rec.Status != "running" {
		t.Errorf("Status = %q", rec.Status)
	}
	if rec.Total != 5 {
		t.Errorf("Total = %d", rec.Total)
	}
	if rec.FinishedAt != nil {
		t.Error("FinishedAt should be nil for running run")
	}
}

func TestGetRun_NotFound(t *testing.T) {
	d := openMemory(t)
	rec, err := d.GetRun("no-such-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec != nil {
		t.Errorf("expected nil, got %+v", rec)
	}
}

func TestGetRun_FinishedAt_Set(t *testing.T) {
	d := openMemory(t)
	_ = d.StartRun("rf", "", 1)
	_ = d.FinishRun("rf", 1, 0, 0)

	rec, err := d.GetRun("rf")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if rec.FinishedAt == nil {
		t.Error("FinishedAt should be set after FinishRun")
	}
	if rec.Status != "done" {
		t.Errorf("Status = %q, want done", rec.Status)
	}
}
