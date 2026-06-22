package cmd

import (
	"strings"
	"testing"

	"github.com/Raina-Hardik/smelt/internal/config"
)

func TestPromptYesNo(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"y\n", true}, {"Y\n", true}, {"yes\n", true}, {"  YES \n", true},
		{"n\n", false}, {"\n", false}, {"", false}, {"maybe\n", false},
	}
	for _, c := range cases {
		got, err := promptYesNo(strings.NewReader(c.in), &strings.Builder{}, "? ")
		if err != nil {
			t.Fatalf("%q: unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("promptYesNo(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestConfirmInplaceSkipsPrompt(t *testing.T) {
	// Non-inplace, --dry-run, and --assume-yes must all proceed without prompting.
	for _, cfg := range []*config.Config{
		{InPlace: false},
		{InPlace: true, DryRun: true},
		{InPlace: true, AssumeYes: true},
	} {
		ok, err := confirmInplace(cfg, 3)
		if err != nil || !ok {
			t.Errorf("confirmInplace(%+v) = (%v, %v), want (true, nil)", cfg, ok, err)
		}
	}
}
