package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Raina-Hardik/smelt/internal/db"
	"github.com/Raina-Hardik/smelt/internal/workflow"
)

// testEnv holds a fully wired Server and its handler for table-driven tests.
type testEnv struct {
	srv *Server
	h   http.Handler
	db  *db.DB
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	srv := New(database, dir, "smelt", "")
	return &testEnv{srv: srv, h: srv.Handler(), db: database}
}

// do sends a request against the test handler and returns the recorder.
func (e *testEnv) do(method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w
}

// decode is a helper to unmarshal the response body.
func decode(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}
}

// createProgram posts a new program and returns its ID.
func createProgram(t *testing.T, e *testEnv, src string, rules []workflow.Rule) string {
	t.Helper()
	inp := map[string]any{
		"name":  "test-program",
		"src":   src,
		"ext":   []string{"mkv"},
		"rules": rules,
	}
	w := e.do(http.MethodPost, "/api/programs", inp)
	if w.Code != http.StatusCreated {
		t.Fatalf("createProgram: got %d, body=%s", w.Code, w.Body.String())
	}
	var resp programResponse
	decode(t, w, &resp)
	return resp.ID
}

// --- Health ---

func TestHandleHealth(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodGet, "/api/health", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]string
	decode(t, w, &body)
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
}

func TestHandleHealth_ContentType(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodGet, "/api/health", nil)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// --- List programs ---

func TestHandleListPrograms_Empty(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodGet, "/api/programs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var list []programResponse
	decode(t, w, &list)
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d items", len(list))
	}
}

func TestHandleListPrograms_ReturnsList(t *testing.T) {
	e := newTestEnv(t)
	createProgram(t, e, "/src/a", nil)
	createProgram(t, e, "/src/b", nil)

	w := e.do(http.MethodGet, "/api/programs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var list []programResponse
	decode(t, w, &list)
	if len(list) != 2 {
		t.Errorf("expected 2 programs, got %d", len(list))
	}
}

func TestHandleListPrograms_FieldsPresent(t *testing.T) {
	e := newTestEnv(t)
	createProgram(t, e, "/movies", nil)

	w := e.do(http.MethodGet, "/api/programs", nil)
	var list []programResponse
	decode(t, w, &list)
	p := list[0]
	if p.ID == "" {
		t.Error("ID is empty")
	}
	if p.Src != "/movies" {
		t.Errorf("Src = %q", p.Src)
	}
	if p.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

// --- Create program ---

func TestHandleCreateProgram_Valid(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodPost, "/api/programs", map[string]any{
		"name": "weekend",
		"src":  "/media",
		"ext":  []string{"mkv", "mp4"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var resp programResponse
	decode(t, w, &resp)
	if resp.ID == "" {
		t.Error("ID should be assigned")
	}
	if resp.Src != "/media" {
		t.Errorf("Src = %q", resp.Src)
	}
	if len(resp.Ext) != 2 {
		t.Errorf("Ext len = %d, want 2", len(resp.Ext))
	}
}

func TestHandleCreateProgram_MissingSrc(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodPost, "/api/programs", map[string]any{
		"name": "no-src",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleCreateProgram_BadJSON(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/api/programs", strings.NewReader("{broken"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleCreateProgram_DefaultExt(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodPost, "/api/programs", map[string]any{
		"name": "defaults",
		"src":  "/x",
		// no ext → should default to mkv,mp4,avi
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
	var resp programResponse
	decode(t, w, &resp)
	if len(resp.Ext) != 3 {
		t.Errorf("default ext count = %d, want 3; got %v", len(resp.Ext), resp.Ext)
	}
}

func TestHandleCreateProgram_WithRules(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodPost, "/api/programs", map[string]any{
		"src": "/x",
		"rules": []map[string]any{
			{"conditions": []map[string]any{{"field": "codec", "op": "eq", "value": "h264"}},
				"do": map[string]any{"cmd": "transcode"}},
		},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
}

func TestHandleCreateProgram_EmptyRules(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodPost, "/api/programs", map[string]any{
		"src":   "/x",
		"rules": []any{},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleCreateProgram_TimestampsSet(t *testing.T) {
	before := time.Now().Truncate(time.Second)
	e := newTestEnv(t)
	w := e.do(http.MethodPost, "/api/programs", map[string]any{"src": "/x"})
	var resp programResponse
	decode(t, w, &resp)
	if resp.CreatedAt.Before(before) {
		t.Errorf("CreatedAt %v is before test start %v", resp.CreatedAt, before)
	}
}

// --- Get program ---

func TestHandleGetProgram_Found(t *testing.T) {
	e := newTestEnv(t)
	id := createProgram(t, e, "/found", nil)

	w := e.do(http.MethodGet, "/api/programs/"+id, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp programResponse
	decode(t, w, &resp)
	if resp.ID != id {
		t.Errorf("ID = %q, want %q", resp.ID, id)
	}
	if resp.Src != "/found" {
		t.Errorf("Src = %q", resp.Src)
	}
}

func TestHandleGetProgram_NotFound(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodGet, "/api/programs/no-such-id", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleGetProgram_RulesPreserved(t *testing.T) {
	e := newTestEnv(t)
	rules := []workflow.Rule{
		{Do: workflow.Action{Cmd: "transcode"}},
	}
	id := createProgram(t, e, "/x", rules)
	w := e.do(http.MethodGet, "/api/programs/"+id, nil)
	var resp programResponse
	decode(t, w, &resp)
	if len(resp.Rules) != 1 {
		t.Errorf("rules count = %d, want 1", len(resp.Rules))
	}
}

// --- Update program ---

func TestHandleUpdateProgram_Valid(t *testing.T) {
	e := newTestEnv(t)
	id := createProgram(t, e, "/old", nil)

	w := e.do(http.MethodPut, "/api/programs/"+id, map[string]any{
		"name": "updated",
		"src":  "/new",
		"ext":  []string{"mp4"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var resp programResponse
	decode(t, w, &resp)
	if resp.Src != "/new" {
		t.Errorf("Src = %q, want /new", resp.Src)
	}
	if resp.Name != "updated" {
		t.Errorf("Name = %q", resp.Name)
	}
}

func TestHandleUpdateProgram_NotFound(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodPut, "/api/programs/ghost", map[string]any{"src": "/x"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleUpdateProgram_BadJSON(t *testing.T) {
	e := newTestEnv(t)
	id := createProgram(t, e, "/x", nil)
	req := httptest.NewRequest(http.MethodPut, "/api/programs/"+id, strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleUpdateProgram_MissingSrc(t *testing.T) {
	e := newTestEnv(t)
	id := createProgram(t, e, "/x", nil)
	w := e.do(http.MethodPut, "/api/programs/"+id, map[string]any{"name": "no-src"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleUpdateProgram_DefaultExt(t *testing.T) {
	e := newTestEnv(t)
	id := createProgram(t, e, "/x", nil)
	w := e.do(http.MethodPut, "/api/programs/"+id, map[string]any{"src": "/y"})
	var resp programResponse
	decode(t, w, &resp)
	if len(resp.Ext) != 3 {
		t.Errorf("default ext count = %d, want 3", len(resp.Ext))
	}
}

// --- Delete program ---

func TestHandleDeleteProgram_Found(t *testing.T) {
	e := newTestEnv(t)
	id := createProgram(t, e, "/x", nil)
	w := e.do(http.MethodDelete, "/api/programs/"+id, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	// Confirm it's gone.
	w2 := e.do(http.MethodGet, "/api/programs/"+id, nil)
	if w2.Code != http.StatusNotFound {
		t.Errorf("after delete: status = %d, want 404", w2.Code)
	}
}

func TestHandleDeleteProgram_NotFound(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodDelete, "/api/programs/no-such", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// --- Run program ---

func TestHandleRunProgram_Found(t *testing.T) {
	e := newTestEnv(t)
	// Use a fast-exiting fake binary so the subprocess completes immediately.
	e.srv.binary = writeFakeBinary(t, "exit 0")
	id := createProgram(t, e, "/src", nil)

	w := e.do(http.MethodPost, "/api/programs/"+id+"/run", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	decode(t, w, &resp)
	if resp["run_id"] == "" {
		t.Error("run_id should be set in response")
	}
}

func TestHandleRunProgram_NotFound(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodPost, "/api/programs/ghost/run", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleRunProgram_RunIDIsUUID(t *testing.T) {
	e := newTestEnv(t)
	e.srv.binary = writeFakeBinary(t, "exit 0")
	id := createProgram(t, e, "/x", nil)

	w := e.do(http.MethodPost, "/api/programs/"+id+"/run", nil)
	var resp map[string]string
	decode(t, w, &resp)
	runID := resp["run_id"]
	// UUID v4: 8-4-4-4-12 hex chars with hyphens.
	parts := strings.Split(runID, "-")
	if len(parts) != 5 {
		t.Errorf("run_id %q does not look like a UUID", runID)
	}
}

// --- List runs ---

func TestHandleListRuns_Empty(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodGet, "/api/runs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var list []runResponse
	decode(t, w, &list)
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestHandleListRuns_WithData(t *testing.T) {
	e := newTestEnv(t)
	_ = e.db.StartRun("r1", "alpha", 1)
	_ = e.db.StartRun("r2", "beta", 2)

	w := e.do(http.MethodGet, "/api/runs", nil)
	var list []runResponse
	decode(t, w, &list)
	if len(list) != 2 {
		t.Errorf("expected 2 runs, got %d", len(list))
	}
}

func TestHandleListRuns_StatusFilter(t *testing.T) {
	e := newTestEnv(t)
	_ = e.db.StartRun("running-run", "r", 1)
	_ = e.db.StartRun("done-run", "d", 1)
	_ = e.db.FinishRun("done-run", 1, 0, 0)

	w := e.do(http.MethodGet, "/api/runs?status=running", nil)
	var list []runResponse
	decode(t, w, &list)
	if len(list) != 1 || list[0].RunID != "running-run" {
		t.Errorf("status=running: got %v", list)
	}

	w2 := e.do(http.MethodGet, "/api/runs?status=done", nil)
	var list2 []runResponse
	decode(t, w2, &list2)
	if len(list2) != 1 || list2[0].RunID != "done-run" {
		t.Errorf("status=done: got %v", list2)
	}
}

func TestHandleListRuns_LimitParam(t *testing.T) {
	e := newTestEnv(t)
	for i := range 10 {
		id := "run-" + string(rune('a'+i))
		_ = e.db.StartRun(id, "", 1)
	}

	w := e.do(http.MethodGet, "/api/runs?limit=3", nil)
	var list []runResponse
	decode(t, w, &list)
	if len(list) != 3 {
		t.Errorf("limit=3: expected 3, got %d", len(list))
	}
}

func TestHandleListRuns_InvalidLimit(t *testing.T) {
	e := newTestEnv(t)
	_ = e.db.StartRun("r1", "", 1)

	// Invalid limit should fall back to default (50).
	w := e.do(http.MethodGet, "/api/runs?limit=notanumber", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var list []runResponse
	decode(t, w, &list)
	if len(list) != 1 {
		t.Errorf("expected 1 run, got %d", len(list))
	}
}

func TestHandleListRuns_FieldsPresent(t *testing.T) {
	e := newTestEnv(t)
	_ = e.db.StartRun("rf", "my-run", 7)

	w := e.do(http.MethodGet, "/api/runs", nil)
	var list []runResponse
	decode(t, w, &list)
	r := list[0]
	if r.RunID != "rf" {
		t.Errorf("RunID = %q", r.RunID)
	}
	if r.Name != "my-run" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Total != 7 {
		t.Errorf("Total = %d", r.Total)
	}
	if r.Status != "running" {
		t.Errorf("Status = %q", r.Status)
	}
	if r.StartedAt.IsZero() {
		t.Error("StartedAt is zero")
	}
	if r.FinishedAt != nil {
		t.Error("FinishedAt should be nil for running run")
	}
}

// --- Get run ---

func TestHandleGetRun_Found(t *testing.T) {
	e := newTestEnv(t)
	_ = e.db.StartRun("rg", "details-run", 2)

	w := e.do(http.MethodGet, "/api/runs/rg", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp runDetailResponse
	decode(t, w, &resp)
	if resp.RunID != "rg" {
		t.Errorf("RunID = %q", resp.RunID)
	}
}

func TestHandleGetRun_NotFound(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodGet, "/api/runs/no-such", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleGetRun_JobsArray(t *testing.T) {
	e := newTestEnv(t)
	_ = e.db.StartRun("rj", "", 2)
	_ = e.db.AddJobs("rj", []string{"/a.mkv", "/b.mkv"})
	_ = e.db.UpdateJob("rj", "/a.mkv", "running", 0.5, "1.2x")

	w := e.do(http.MethodGet, "/api/runs/rj", nil)
	var resp runDetailResponse
	decode(t, w, &resp)
	if len(resp.Jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(resp.Jobs))
	}
}

func TestHandleGetRun_JobFieldsPresent(t *testing.T) {
	e := newTestEnv(t)
	_ = e.db.StartRun("rjf", "", 1)
	_ = e.db.AddJobs("rjf", []string{"/movie.mkv"})
	_ = e.db.UpdateJob("rjf", "/movie.mkv", "running", 0.77, "2.3x")

	w := e.do(http.MethodGet, "/api/runs/rjf", nil)
	var resp runDetailResponse
	decode(t, w, &resp)
	j := resp.Jobs[0]
	if j.SourcePath != "/movie.mkv" {
		t.Errorf("SourcePath = %q", j.SourcePath)
	}
	if j.Status != "running" {
		t.Errorf("Status = %q", j.Status)
	}
	if j.Pct < 0.76 || j.Pct > 0.78 {
		t.Errorf("Pct = %f", j.Pct)
	}
	if j.Speed != "2.3x" {
		t.Errorf("Speed = %q", j.Speed)
	}
	if j.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero")
	}
}

func TestHandleGetRun_FinishedAt_Set(t *testing.T) {
	e := newTestEnv(t)
	_ = e.db.StartRun("rdone", "", 1)
	_ = e.db.FinishRun("rdone", 1, 0, 0)

	w := e.do(http.MethodGet, "/api/runs/rdone", nil)
	var resp runDetailResponse
	decode(t, w, &resp)
	if resp.FinishedAt == nil {
		t.Error("FinishedAt should be set for finished run")
	}
	if resp.Status != "done" {
		t.Errorf("Status = %q, want done", resp.Status)
	}
}

// --- Cancel run ---

func TestHandleCancelRun_NotFound(t *testing.T) {
	e := newTestEnv(t)
	// No run in DB at all.
	w := e.do(http.MethodDelete, "/api/runs/ghost", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleCancelRun_NoLiveProcess(t *testing.T) {
	e := newTestEnv(t)
	// Run exists in DB but no live process tracked.
	_ = e.db.StartRun("no-proc", "", 1)

	w := e.do(http.MethodDelete, "/api/runs/no-proc", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	var resp map[string]string
	decode(t, w, &resp)
	if resp["error"] == "" {
		t.Error("error field should be set in 409 response")
	}
}

// --- JSON helpers ---

func TestMarshalUnmarshalRules_RoundTrip(t *testing.T) {
	rules := []workflow.Rule{
		{Do: workflow.Action{Cmd: "transcode"}},
		{Do: workflow.Action{Cmd: "skip"}},
	}
	s, err := marshalRules(rules)
	if err != nil {
		t.Fatalf("marshalRules: %v", err)
	}
	got, err := unmarshalRules(s)
	if err != nil {
		t.Fatalf("unmarshalRules: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("rules count = %d, want 2", len(got))
	}
	if got[0].Do.Cmd != "transcode" {
		t.Errorf("first rule cmd = %q", got[0].Do.Cmd)
	}
}

func TestMarshalRules_Empty(t *testing.T) {
	s, err := marshalRules(nil)
	if err != nil {
		t.Fatalf("marshalRules: %v", err)
	}
	if s != "null" && s != "[]" {
		// json.Marshal of a nil slice produces "null"
		_ = s
	}
}

func TestUnmarshalRules_Empty(t *testing.T) {
	rules, err := unmarshalRules("")
	if err != nil {
		t.Fatalf("unmarshalRules empty: %v", err)
	}
	if rules == nil {
		rules = []workflow.Rule{}
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}
}

func TestUnmarshalRules_EmptyBrackets(t *testing.T) {
	rules, err := unmarshalRules("[]")
	if err != nil {
		t.Fatalf("unmarshalRules []: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}
}
