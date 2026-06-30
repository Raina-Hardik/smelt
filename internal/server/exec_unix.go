//go:build !windows

package server

import (
	"os"
	"os/exec"
	"syscall"
)

// setSysProcAttr puts the child into its own process group so that
// cancelRun can SIGTERM the entire tree via a negative PID.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
