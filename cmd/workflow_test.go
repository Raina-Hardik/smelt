package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestWorkflowScheduleRequiresOut(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset() })

	rootCmd.SetArgs([]string{"workflow", "--src", "/tmp", "--schedule", "0 3 * * *"})
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)

	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--schedule requires --out") {
		t.Fatalf("expected schedule-requires-out error, got %v", err)
	}
}
