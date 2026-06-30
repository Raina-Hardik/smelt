//go:build windows

package server

import (
	"os"
	"os/exec"
)

// setSysProcAttr is a no-op on Windows; process groups work differently and
// smelt serve is primarily a Linux/macOS server process.
func setSysProcAttr(_ *exec.Cmd) {}

// cancelRun kills the tracked process directly. On Windows we cannot send
// SIGTERM to a process group, so we call Kill() on the process itself.
// Returns false if no live process was found for runID.
func (s *Server) cancelRun(runID string) bool {
	v, ok := s.procs.Load(runID)
	if !ok {
		return false
	}
	proc := v.(*os.Process)
	_ = proc.Kill()
	return true
}
