package workflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateProgramInput(t *testing.T) {
	ok := []Rule{{Do: Action{Cmd: "transcode", Args: []string{"--codec", "h265", "--crf=24", "'h265'"}}}}
	if err := ValidateProgramInput("n", "0 3 * * *", "/media", []string{"mkv"}, ok); err != nil {
		t.Errorf("valid input rejected: %v", err)
	}
	bad := []struct {
		name string
		fn   func() error
	}{
		{"arg with ;", func() error {
			return ValidateProgramInput("n", "", "/m", nil, []Rule{{Do: Action{Cmd: "check", Args: []string{"; id"}}}})
		}},
		{"arg with $()", func() error {
			return ValidateProgramInput("n", "", "/m", nil, []Rule{{Do: Action{Cmd: "check", Args: []string{"$(id)"}}}})
		}},
		{"embedded quote", func() error {
			return ValidateProgramInput("n", "", "/m", nil, []Rule{{Do: Action{Cmd: "check", Args: []string{"'a'b'"}}}})
		}},
		{"unknown cmd", func() error {
			return ValidateProgramInput("n", "", "/m", nil, []Rule{{Do: Action{Cmd: "sh"}}})
		}},
		{"newline name", func() error { return ValidateProgramInput("a\nb", "", "/m", nil, nil) }},
		{"newline src", func() error { return ValidateProgramInput("n", "", "/m\nid", nil, nil) }},
		{"NUL schedule", func() error { return ValidateProgramInput("n", "x\x00", "/m", nil, nil) }},
		{"space in ext", func() error { return ValidateProgramInput("n", "", "/m", []string{"m kv"}, nil) }},
	}
	for _, c := range bad {
		if c.fn() == nil {
			t.Errorf("%s: expected rejection", c.name)
		}
	}
}

// The program name is echoed inside the generated script; hostile names must
// stay inert even if they reach Render (e.g. via a hand-edited script).
func TestRenderQuotesProgramName(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	name := "x$(touch " + marker + ")`touch " + marker + "`\"; touch " + marker + "; \""
	script := Render(Program{Name: name, Src: "/nonexistent", Ext: []string{"mkv"}}, Options{Binary: "/bin/true", Now: time.Unix(0, 0)})

	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock not available")
	}
	dir := t.TempDir()
	lock := filepath.Join(dir, "smelt-"+sanitize(name)+".lock")
	holder := exec.Command("flock", "-x", lock, "sleep", "5")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()
	time.Sleep(300 * time.Millisecond) // let the holder take the lock

	path := filepath.Join(dir, "p.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", path)
	cmd.Env = append(os.Environ(), "TMPDIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "already running") {
		t.Fatalf("flock-busy branch not exercised; output: %q", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("shell metacharacters in the program name were executed")
	}
}
