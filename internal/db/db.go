package db

import (
	"database/sql"
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
