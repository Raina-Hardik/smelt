package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Raina-Hardik/smelt/internal/workflow"
	"github.com/rs/zerolog/log"
)

// triggerRun renders p to a script, writes it to scriptsDir, and starts it as
// a subprocess with SMELT_RUN_ID=runID. The process is tracked in s.procs so
// it can be cancelled via cancelRun. Returns immediately after the process
// starts; the script runs to completion in the background.
func (s *Server) triggerRun(runID string, p workflow.Program) error {
	bin, _ := os.Executable()
	if s.binary != "" {
		bin = s.binary
	}

	script := workflow.Render(p, workflow.Options{
		Binary:  bin,
		Version: "",
		Now:     time.Now(),
	})

	scriptPath := filepath.Join(s.scriptsDir, "smelt-"+runID+".sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("write script: %w", err)
	}

	logPath := scriptPath + ".log"
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	cmd := exec.Command("sh", scriptPath)
	cmd.Env = append(os.Environ(), "SMELT_RUN_ID="+runID)
	cmd.Stdout = lf
	cmd.Stderr = lf
	// Start in its own process group so SIGTERM reaches all children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		_ = lf.Close()
		return fmt.Errorf("start script: %w", err)
	}

	s.procs.Store(runID, cmd.Process)
	log.Info().Str("run_id", runID).Str("script", scriptPath).Msg("program run started")

	go func() {
		_ = cmd.Wait()
		_ = lf.Close()
		s.procs.Delete(runID)
		log.Info().Str("run_id", runID).Msg("program run finished")
	}()

	return nil
}

// cancelRun sends SIGTERM to the process group of the running script.
// Returns false if no live process was found for runID.
func (s *Server) cancelRun(runID string) bool {
	v, ok := s.procs.Load(runID)
	if !ok {
		return false
	}
	proc := v.(*os.Process)
	// Negative PID targets the process group (set via Setpgid above).
	_ = syscall.Kill(-proc.Pid, syscall.SIGTERM)
	return true
}
