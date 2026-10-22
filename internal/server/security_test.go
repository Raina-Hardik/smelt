package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// Program definitions arrive from an unauthenticated API and are rendered
// into a shell script that smelt executes: hostile input must be rejected.
func TestCreateProgramRejectsInjection(t *testing.T) {
	e := newTestEnv(t)
	do := func(action map[string]any, mutate func(map[string]any)) int {
		body := map[string]any{"src": "/x", "rules": []map[string]any{{"when": []any{}, "do": action}}}
		if mutate != nil {
			mutate(body)
		}
		return e.do(http.MethodPost, "/api/programs", body).Code
	}
	bad := map[string]func() int{
		"semicolon arg":   func() int { return do(map[string]any{"cmd": "check", "args": []string{"|| true; id"}}, nil) },
		"command subst":   func() int { return do(map[string]any{"cmd": "check", "args": []string{"$(id)"}}, nil) },
		"backtick":        func() int { return do(map[string]any{"cmd": "check", "args": []string{"`id`"}}, nil) },
		"unterminated '":  func() int { return do(map[string]any{"cmd": "check", "args": []string{"'; id #"}}, nil) },
		"newline arg":     func() int { return do(map[string]any{"cmd": "check", "args": []string{"a\nid"}}, nil) },
		"unknown cmd":     func() int { return do(map[string]any{"cmd": "rm", "args": []string{}}, nil) },
		"newline in src":  func() int { return do(map[string]any{"cmd": "skip"}, func(b map[string]any) { b["src"] = "/x\nid" }) },
		"newline in name": func() int { return do(map[string]any{"cmd": "skip"}, func(b map[string]any) { b["name"] = "a\nid" }) },
		"newline in sched": func() int {
			return do(map[string]any{"cmd": "skip"}, func(b map[string]any) { b["schedule"] = "* * * * *\nid" })
		},
		"newline in ext": func() int {
			return do(map[string]any{"cmd": "skip"}, func(b map[string]any) { b["ext"] = []string{"mkv\nid"} })
		},
		"newline in cond": func() int {
			return do(map[string]any{"cmd": "skip"}, func(b map[string]any) {
				b["rules"] = []map[string]any{{"when": []map[string]any{{"field": "codec", "op": "eq", "value": "h264\nid"}}, "do": map[string]any{"cmd": "skip"}}}
			})
		},
	}
	for name, f := range bad {
		if code := f(); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, code)
		}
	}
	for name, args := range map[string][]string{
		"plain flags":  {"--codec", "h265", "--crf", "24"},
		"quoted token": {"--codec", "'h265'"},
		"key=value":    {"--crf=24", "--ext", "mkv,mp4"},
	} {
		if code := do(map[string]any{"cmd": "transcode", "args": args}, nil); code != http.StatusCreated {
			t.Errorf("%s should be accepted, status = %d", name, code)
		}
	}
}

func TestDecodeJSONRequiresJSONContentType(t *testing.T) {
	e := newTestEnv(t)
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		req := httptest.NewRequest(http.MethodPost, "/api/programs", strings.NewReader(`{"src":"/x"}`))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, req)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: status = %d, want 415", ct, w.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/programs", strings.NewReader(`{"src":"/x"}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("json with charset: status = %d, want 201", w.Code)
	}
}

func TestGuard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	cases := []struct {
		name, addr, host, origin string
		want                     int
	}{
		{"cli on loopback", "127.0.0.1:7700", "127.0.0.1:7700", "", 200},
		{"localhost host", "127.0.0.1:7700", "localhost:7700", "", 200},
		{"ipv6 loopback", "[::1]:7700", "[::1]:7700", "", 200},
		{"same-origin browser", "127.0.0.1:7700", "127.0.0.1:7700", "http://127.0.0.1:7700", 200},
		{"cross-site browser", "127.0.0.1:7700", "127.0.0.1:7700", "http://evil.example", 403},
		{"dns rebinding", "127.0.0.1:7700", "evil.example:7700", "", 403},
		{"rebinding with matching origin", "127.0.0.1:7700", "evil.example:7700", "http://evil.example:7700", 403},
		{"non-loopback bind skips host check", "0.0.0.0:7700", "myserver.lan:7700", "", 200},
		{"non-loopback bind still blocks cross-site", "0.0.0.0:7700", "myserver.lan:7700", "http://evil.example", 403},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/programs", nil)
		req.Host = c.host
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		guard(ok, c.addr).ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, w.Code, c.want)
		}
	}
}

// A program already in the database (stored before validation existed, or
// written directly) must not be rendered into an executable script.
func TestRunRefusesHostileStoredProgram(t *testing.T) {
	e := newTestEnv(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	rules := `[{"when":[],"do":{"cmd":"check","args":["|| true; touch ` + marker + ` ;"]}}]`
	if err := e.db.CreateProgram("legacy", "legacy", "", "/nonexistent", "mkv", rules); err != nil {
		t.Fatal(err)
	}
	w := e.do(http.MethodPost, "/api/programs/legacy/run", nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d (%s), want 422", w.Code, w.Body.String())
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("hostile stored program was executed")
	}
	if left, _ := filepath.Glob(filepath.Join(e.srv.scriptsDir, "smelt-*.sh")); len(left) != 0 {
		t.Errorf("no script should have been written: %v", left)
	}

	// A clean stored program still runs.
	good := `[{"when":[],"do":{"cmd":"skip","args":[]}}]`
	if err := e.db.CreateProgram("ok", "ok", "", "/nonexistent", "mkv", good); err != nil {
		t.Fatal(err)
	}
	if w := e.do(http.MethodPost, "/api/programs/ok/run", nil); w.Code != http.StatusAccepted {
		t.Errorf("clean program: status = %d (%s), want 202", w.Code, w.Body.String())
	}
}

// Listening beyond loopback with no authentication deserves a loud warning.
func TestWarnIfExposed(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:7700": false,
		"localhost:7700": false,
		"[::1]:7700":     false,
		"0.0.0.0:7700":   true,
		":7700":          true,
		"192.168.1.5:80": true,
		"example.com:80": true,
	}
	for addr, want := range cases {
		var buf bytes.Buffer
		warnIfExposed(zerolog.New(&buf), addr)
		if got := strings.Contains(buf.String(), "no authentication"); got != want {
			t.Errorf("%s: warned = %v, want %v", addr, got, want)
		}
	}
}
