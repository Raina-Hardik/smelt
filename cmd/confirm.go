package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Raina-Hardik/smelt/internal/config"
)

// confirmInplace prompts before a destructive --inplace run, unless suppressed
// by -y/--assume-yes or --dry-run. Returns true when it is safe to proceed.
// A non-interactive stdin (EOF) defaults to "no" — scripts must pass -y.
func confirmInplace(cfg *config.Config, n int) (bool, error) {
	if !cfg.InPlace || cfg.AssumeYes || cfg.DryRun {
		return true, nil
	}
	prompt := fmt.Sprintf("--inplace will permanently replace %d original file(s). Continue? [y/N] ", n)
	return promptYesNo(os.Stdin, os.Stderr, prompt)
}

func promptYesNo(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
