package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite connection.
type DB struct{ db *sql.DB }

// Record captures one completed (or failed) transcode operation.
type Record struct {
	SourcePath  string
	SourceMtime int64
	SourceSize  int64
	SourceCodec string
	OutputPath  string
	OutputSize  int64
	TargetCodec string
	Encoder     string
	Backend     string
	CRF         int
	Preset      string
	DurationMs  int64
	ElapsedMs   int64
	Status      string // "done" | "failed"
	ErrorMsg    string
	CompletedAt time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS transcodes (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    source_path  TEXT    NOT NULL,
    source_mtime INTEGER NOT NULL,
    source_size  INTEGER NOT NULL DEFAULT 0,
    source_codec TEXT    NOT NULL DEFAULT '',
    output_path  TEXT    NOT NULL,
    output_size  INTEGER NOT NULL DEFAULT 0,
    target_codec TEXT    NOT NULL,
    encoder      TEXT    NOT NULL,
    backend      TEXT    NOT NULL DEFAULT '',
    crf          INTEGER NOT NULL DEFAULT 0,
    preset       TEXT    NOT NULL DEFAULT '',
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    elapsed_ms   INTEGER NOT NULL DEFAULT 0,
    status       TEXT    NOT NULL CHECK(status IN ('done','failed')),
    error_msg    TEXT    NOT NULL DEFAULT '',
    completed_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_source ON transcodes(source_path, source_mtime);
CREATE INDEX IF NOT EXISTS idx_completed ON transcodes(completed_at);

CREATE TABLE IF NOT EXISTS runs (
    run_id      TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL DEFAULT '',
    started_at  INTEGER NOT NULL,
    finished_at INTEGER,
    status      TEXT    NOT NULL DEFAULT 'running'
                        CHECK(status IN ('running','done','failed','cancelled')),
    total       INTEGER NOT NULL DEFAULT 0,
    ok          INTEGER NOT NULL DEFAULT 0,
    failed      INTEGER NOT NULL DEFAULT 0,
    skipped     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);

CREATE TABLE IF NOT EXISTS jobs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      TEXT    NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    source_path TEXT    NOT NULL,
    status      TEXT    NOT NULL DEFAULT 'queued'
                        CHECK(status IN ('queued','running','done','failed','skipped')),
    pct         REAL    NOT NULL DEFAULT 0,
    speed       TEXT    NOT NULL DEFAULT '',
    updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_run ON jobs(run_id, status);
`

// Open opens (or creates) the SQLite database at path.
// WAL mode is enabled for concurrent reads during writes.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := conn.Exec(pragma); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	if _, err := conn.Exec(schema); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &DB{db: conn}, nil
}

// Close closes the underlying connection.
func (d *DB) Close() error { return d.db.Close() }

// Insert records the outcome of a transcode operation.
func (d *DB) Insert(r Record) error {
	_, err := d.db.Exec(`
		INSERT INTO transcodes
			(source_path, source_mtime, source_size, source_codec,
			 output_path, output_size, target_codec, encoder, backend,
			 crf, preset, duration_ms, elapsed_ms,
			 status, error_msg, completed_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.SourcePath, r.SourceMtime, r.SourceSize, r.SourceCodec,
		r.OutputPath, r.OutputSize, r.TargetCodec, r.Encoder, r.Backend,
		r.CRF, r.Preset, r.DurationMs, r.ElapsedMs,
		r.Status, r.ErrorMsg, r.CompletedAt.Unix(),
	)
	return err
}

// IsDone reports whether path has a 'done' transcode record with a matching
// mtime, meaning the file was successfully transcoded and hasn't changed since.
func (d *DB) IsDone(path string, mtime int64) bool {
	var n int
	err := d.db.QueryRow(
		`SELECT 1 FROM transcodes WHERE source_path=? AND source_mtime=? AND status='done' LIMIT 1`,
		path, mtime,
	).Scan(&n)
	return err == nil && n == 1
}

// Recent returns the most recent records. Pass failedOnly=true to restrict to
// failed transcodes. The result is ordered newest-first.
func (d *DB) Recent(limit int, failedOnly bool) ([]Record, error) {
	q := `SELECT source_path, source_mtime, source_size, source_codec,
		         output_path, output_size, target_codec, encoder, backend,
		         crf, preset, duration_ms, elapsed_ms,
		         status, error_msg, completed_at
		  FROM transcodes`
	if failedOnly {
		q += ` WHERE status='failed'`
	}
	q += ` ORDER BY completed_at DESC LIMIT ?`

	rows, err := d.db.Query(q, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Record
	for rows.Next() {
		var r Record
		var completedAt int64
		if err := rows.Scan(
			&r.SourcePath, &r.SourceMtime, &r.SourceSize, &r.SourceCodec,
			&r.OutputPath, &r.OutputSize, &r.TargetCodec, &r.Encoder, &r.Backend,
			&r.CRF, &r.Preset, &r.DurationMs, &r.ElapsedMs,
			&r.Status, &r.ErrorMsg, &completedAt,
		); err != nil {
			return nil, err
		}
		r.CompletedAt = time.Unix(completedAt, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunRecord is a row from the runs table.
type RunRecord struct {
	RunID      string
	Name       string
	StartedAt  time.Time
	FinishedAt *time.Time
	Status     string
	Total      int
	OK         int
	Failed     int
	Skipped    int
}

// JobRecord is a row from the jobs table.
type JobRecord struct {
	ID         int64
	RunID      string
	SourcePath string
	Status     string
	Pct        float64
	Speed      string
	UpdatedAt  time.Time
}

// StartRun inserts a run row with status='running'. Call before dispatching files.
func (d *DB) StartRun(runID, name string, total int) error {
	_, err := d.db.Exec(
		`INSERT INTO runs (run_id, name, started_at, status, total) VALUES (?,?,?,?,?)`,
		runID, name, time.Now().Unix(), "running", total,
	)
	return err
}

// FinishRun updates a run's counters and marks it done or failed.
func (d *DB) FinishRun(runID string, ok, failed, skipped int) error {
	status := "done"
	if failed > 0 {
		status = "failed"
	}
	_, err := d.db.Exec(
		`UPDATE runs SET status=?, ok=?, failed=?, skipped=?, finished_at=? WHERE run_id=?`,
		status, ok, failed, skipped, time.Now().Unix(), runID,
	)
	return err
}

// CancelRun marks a run as cancelled.
func (d *DB) CancelRun(runID string) error {
	_, err := d.db.Exec(
		`UPDATE runs SET status='cancelled', finished_at=? WHERE run_id=?`,
		time.Now().Unix(), runID,
	)
	return err
}

// ReconcileAndFinishRun closes a run by deriving its counts from the jobs table.
// Jobs still 'queued' (no rule matched them) become 'skipped'; jobs left
// 'running' (a worker died mid-encode) become 'failed'. The run's ok/failed/
// skipped counters and final status are then set from the reconciled job rows.
func (d *DB) ReconcileAndFinishRun(runID string) error {
	now := time.Now().Unix()
	if _, err := d.db.Exec(
		`UPDATE jobs SET status='skipped', updated_at=? WHERE run_id=? AND status='queued'`,
		now, runID,
	); err != nil {
		return fmt.Errorf("reconcile queued jobs: %w", err)
	}
	if _, err := d.db.Exec(
		`UPDATE jobs SET status='failed', updated_at=? WHERE run_id=? AND status='running'`,
		now, runID,
	); err != nil {
		return fmt.Errorf("reconcile running jobs: %w", err)
	}

	countBy := func(status string) (int, error) {
		var n int
		err := d.db.QueryRow(
			`SELECT COUNT(*) FROM jobs WHERE run_id=? AND status=?`, runID, status,
		).Scan(&n)
		return n, err
	}
	ok, err := countBy("done")
	if err != nil {
		return fmt.Errorf("count done: %w", err)
	}
	failed, err := countBy("failed")
	if err != nil {
		return fmt.Errorf("count failed: %w", err)
	}
	skipped, err := countBy("skipped")
	if err != nil {
		return fmt.Errorf("count skipped: %w", err)
	}

	status := "done"
	if failed > 0 {
		status = "failed"
	}
	if _, err := d.db.Exec(
		`UPDATE runs SET status=?, ok=?, failed=?, skipped=?, finished_at=? WHERE run_id=?`,
		status, ok, failed, skipped, now, runID,
	); err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return nil
}

// AddJobs inserts a 'queued' job row for each path in a single transaction.
func (d *DB) AddJobs(runID string, paths []string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, p := range paths {
		if _, err := tx.Exec(
			`INSERT INTO jobs (run_id, source_path, status, updated_at) VALUES (?,?,?,?)`,
			runID, p, "queued", now,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// UpdateJob upserts progress for a file within a run.
func (d *DB) UpdateJob(runID, sourcePath, status string, pct float64, speed string) error {
	_, err := d.db.Exec(`
		UPDATE jobs SET status=?, pct=?, speed=?, updated_at=?
		WHERE run_id=? AND source_path=?`,
		status, pct, speed, time.Now().Unix(), runID, sourcePath,
	)
	return err
}

// ActiveRuns returns all runs with status='running', newest first.
func (d *DB) ActiveRuns() ([]RunRecord, error) {
	rows, err := d.db.Query(
		`SELECT run_id, name, started_at, finished_at, status, total, ok, failed, skipped
		 FROM runs WHERE status='running' ORDER BY started_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRunRows(rows)
}

// LiveJobs returns all job rows for a run, ordered by source_path.
func (d *DB) LiveJobs(runID string) ([]JobRecord, error) {
	rows, err := d.db.Query(
		`SELECT id, run_id, source_path, status, pct, speed, updated_at
		 FROM jobs WHERE run_id=? ORDER BY source_path`,
		runID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []JobRecord
	for rows.Next() {
		var j JobRecord
		var updatedAt int64
		if err := rows.Scan(&j.ID, &j.RunID, &j.SourcePath, &j.Status, &j.Pct, &j.Speed, &updatedAt); err != nil {
			return nil, err
		}
		j.UpdatedAt = time.Unix(updatedAt, 0)
		out = append(out, j)
	}
	return out, rows.Err()
}

// SpaceSaved returns the aggregate bytes saved across all completed transcodes
// where the output is smaller than the source.
func (d *DB) SpaceSaved() (int64, error) {
	var n int64
	err := d.db.QueryRow(
		`SELECT COALESCE(SUM(source_size - output_size), 0)
		 FROM transcodes
		 WHERE status='done' AND output_size < source_size AND output_size > 0`,
	).Scan(&n)
	return n, err
}

func scanRunRows(rows *sql.Rows) ([]RunRecord, error) {
	var out []RunRecord
	for rows.Next() {
		var r RunRecord
		var startedAt int64
		var finishedAt *int64
		if err := rows.Scan(
			&r.RunID, &r.Name, &startedAt, &finishedAt,
			&r.Status, &r.Total, &r.OK, &r.Failed, &r.Skipped,
		); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(startedAt, 0)
		if finishedAt != nil {
			t := time.Unix(*finishedAt, 0)
			r.FinishedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DefaultPath returns the platform-appropriate default database path.
//
// XDG_DATA_HOME is respected on all platforms. Platform fallbacks:
//   - Linux/other: ~/.local/share/smelt/history.db
//   - macOS:       ~/Library/Application Support/smelt/history.db
//   - Windows:     %LocalAppData%\smelt\history.db
func DefaultPath() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "smelt", "history.db")
	}
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "smelt", "history.db")
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "smelt", "history.db")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "smelt", "history.db")
	}
	return "smelt-history.db"
}
