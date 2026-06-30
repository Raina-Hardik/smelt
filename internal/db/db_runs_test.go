package db

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openMemory(t *testing.T) *DB {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open :memory:: %v", err)
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := conn.Exec(pragma); err != nil {
			t.Fatalf("pragma: %v", err)
		}
	}
	if _, err := conn.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &DB{db: conn}
}

func TestStartAndFinishRun(t *testing.T) {
	d := openMemory(t)

	if err := d.StartRun("run-1", "nightly", 10); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	runs, err := d.ActiveRuns()
	if err != nil {
		t.Fatalf("ActiveRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != "run-1" || runs[0].Status != "running" {
		t.Fatalf("unexpected runs: %+v", runs)
	}

	if err := d.FinishRun("run-1", 8, 2, 0); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	runs, err = d.ActiveRuns()
	if err != nil {
		t.Fatalf("ActiveRuns after finish: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no active runs after finish, got %d", len(runs))
	}
}

func TestAddAndUpdateJobs(t *testing.T) {
	d := openMemory(t)

	if err := d.StartRun("run-2", "", 2); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	paths := []string{"/a.mkv", "/b.mkv"}
	if err := d.AddJobs("run-2", paths); err != nil {
		t.Fatalf("AddJobs: %v", err)
	}

	jobs, err := d.LiveJobs("run-2")
	if err != nil {
		t.Fatalf("LiveJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	for _, j := range jobs {
		if j.Status != "queued" {
			t.Errorf("job %s status = %q, want queued", j.SourcePath, j.Status)
		}
	}

	if err := d.UpdateJob("run-2", "/a.mkv", "running", 0.42, "2.1x"); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	jobs, _ = d.LiveJobs("run-2")
	for _, j := range jobs {
		if j.SourcePath == "/a.mkv" {
			if j.Status != "running" || j.Pct < 0.41 || j.Speed != "2.1x" {
				t.Errorf("unexpected job state: %+v", j)
			}
		}
	}
}

func TestSpaceSaved(t *testing.T) {
	d := openMemory(t)

	now := time.Now().Unix()
	// 100 bytes saved
	_, err := d.db.Exec(`INSERT INTO transcodes
		(source_path,source_mtime,source_size,output_path,output_size,target_codec,encoder,status,completed_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		"/a.mkv", now, 1000, "/a.smelt.mkv", 900, "hevc", "hevc_nvenc", "done", now)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	// output larger — should not count
	_, err = d.db.Exec(`INSERT INTO transcodes
		(source_path,source_mtime,source_size,output_path,output_size,target_codec,encoder,status,completed_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		"/b.mkv", now, 500, "/b.smelt.mkv", 600, "hevc", "hevc_nvenc", "done", now)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	saved, err := d.SpaceSaved()
	if err != nil {
		t.Fatalf("SpaceSaved: %v", err)
	}
	if saved != 100 {
		t.Errorf("SpaceSaved = %d, want 100", saved)
	}
}

func TestReconcileAndFinishRun(t *testing.T) {
	d := openMemory(t)
	if err := d.StartRun("run-r", "", 4); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := d.AddJobs("run-r", []string{"/a.mkv", "/b.mkv", "/c.mkv", "/d.mkv"}); err != nil {
		t.Fatalf("AddJobs: %v", err)
	}
	// a done, b failed, c left running (simulating a crash), d left queued (no rule matched)
	_ = d.UpdateJob("run-r", "/a.mkv", "done", 1.0, "")
	_ = d.UpdateJob("run-r", "/b.mkv", "failed", 1.0, "")
	_ = d.UpdateJob("run-r", "/c.mkv", "running", 0.5, "1x")

	if err := d.ReconcileAndFinishRun("run-r"); err != nil {
		t.Fatalf("ReconcileAndFinishRun: %v", err)
	}

	// Run should be closed (not active) with failed>0 → status failed.
	if active, _ := d.ActiveRuns(); len(active) != 0 {
		t.Errorf("run should not be active after finish")
	}

	jobs, _ := d.LiveJobs("run-r")
	got := map[string]string{}
	for _, j := range jobs {
		got[j.SourcePath] = j.Status
	}
	if got["/c.mkv"] != "failed" {
		t.Errorf("leftover running job should become failed, got %q", got["/c.mkv"])
	}
	if got["/d.mkv"] != "skipped" {
		t.Errorf("queued job should become skipped, got %q", got["/d.mkv"])
	}

	var status string
	var ok, failed, skipped int
	err := d.db.QueryRow(`SELECT status, ok, failed, skipped FROM runs WHERE run_id='run-r'`).
		Scan(&status, &ok, &failed, &skipped)
	if err != nil {
		t.Fatalf("query run: %v", err)
	}
	if status != "failed" || ok != 1 || failed != 2 || skipped != 1 {
		t.Errorf("run counts: status=%s ok=%d failed=%d skipped=%d; want failed/1/2/1", status, ok, failed, skipped)
	}
}

func TestCancelRun(t *testing.T) {
	d := openMemory(t)
	if err := d.StartRun("run-3", "", 5); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := d.CancelRun("run-3"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	runs, _ := d.ActiveRuns()
	if len(runs) != 0 {
		t.Fatalf("cancelled run should not appear in ActiveRuns")
	}
}
