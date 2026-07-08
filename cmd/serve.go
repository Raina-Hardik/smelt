package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Raina-Hardik/smelt/internal/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the smelt HTTP API server",
	Long: `Start the smelt HTTP API server. Stores programs in the history database,
renders them to shell scripts on demand, and executes them as background
subprocesses with SMELT_RUN_ID set. The dashboard WebUI connects to this server
to manage programs, trigger runs, and watch live progress.

API routes:
  GET    /api/health
  GET    /api/programs
  POST   /api/programs
  GET    /api/programs/{id}
  PUT    /api/programs/{id}
  DELETE /api/programs/{id}
  POST   /api/programs/{id}/run
  GET    /api/runs               ?limit=N&status=running|done|failed
  GET    /api/runs/{id}
  DELETE /api/runs/{id}          (cancel: SIGTERM to subprocess)
  GET    /openapi.yaml           the API contract (source of truth)
  GET    /docs                   Scalar API reference (dev builds only)`,
	Example: `  smelt serve
  smelt serve --addr 0.0.0.0:7700
  smelt serve --addr 127.0.0.1:7700 --scripts-dir /var/lib/smelt/scripts`,
	RunE:         runServe,
	SilenceUsage: true,
}

func init() {
	serveCmd.Flags().String("addr", "127.0.0.1:7700", "listen address")
	serveCmd.Flags().String("scripts-dir", "", "directory to write rendered program scripts (default: system temp dir)")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, _ []string) error {
	addr, _ := cmd.Flags().GetString("addr")
	scriptsDir, _ := cmd.Flags().GetString("scripts-dir")

	if scriptsDir == "" {
		scriptsDir = filepath.Join(os.TempDir(), "smelt-scripts")
	}
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		return fmt.Errorf("create scripts dir %s: %w", scriptsDir, err)
	}

	dbPath := viper.GetString("smelt.db")
	database, err := openDB()
	if err != nil {
		return fmt.Errorf("open history db: %w", err)
	}
	if database == nil {
		return fmt.Errorf("history DB is required for serve (--db must not be empty)")
	}
	defer func() { _ = database.Close() }()

	bin, err := os.Executable()
	if err != nil {
		bin = "smelt"
	}

	srv := server.New(database, scriptsDir, bin, dbPath)
	return srv.Start(addr)
}
