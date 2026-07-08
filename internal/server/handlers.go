package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Raina-Hardik/smelt/api"
	"github.com/Raina-Hardik/smelt/internal/workflow"
	"github.com/google/uuid"
)

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, api.Error{Error: msg})
}

func decodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// Aliases to the generated wire types, named for what handlers_test.go
// already expects.
type (
	programResponse   = api.Program
	runResponse       = api.Run
	runDetailResponse = api.RunDetail
)

// --- Conversion helpers ---

func marshalRules(rules []workflow.Rule) (string, error) {
	b, err := json.Marshal(rules)
	return string(b), err
}

func unmarshalRules(s string) ([]workflow.Rule, error) {
	var rules []workflow.Rule
	if s == "" || s == "[]" {
		return rules, nil
	}
	err := json.Unmarshal([]byte(s), &rules)
	return rules, err
}

// normalizedInput is the decoded ProgramInput with its optional fields
// resolved to the same defaults the handlers have always applied.
type normalizedInput struct {
	name     string
	schedule string
	src      string
	ext      []string
	rules    []workflow.Rule
}

func normalizeInput(inp api.ProgramInput) normalizedInput {
	n := normalizedInput{src: inp.Src}
	if inp.Name != nil {
		n.name = *inp.Name
	}
	if inp.Schedule != nil {
		n.schedule = *inp.Schedule
	}
	if inp.Ext != nil {
		n.ext = *inp.Ext
	}
	if len(n.ext) == 0 {
		n.ext = []string{"mkv", "mp4", "avi"}
	}
	if inp.Rules != nil {
		n.rules = *inp.Rules
	}
	return n
}

// --- Handlers (api.ServerInterface) ---

func (s *Server) GetHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, api.Health{Status: "ok"})
}

func (s *Server) ListPrograms(w http.ResponseWriter, _ *http.Request) {
	recs, err := s.db.ListPrograms()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]api.Program, 0, len(recs))
	for _, rec := range recs {
		rules, _ := unmarshalRules(rec.RulesJSON)
		out = append(out, api.Program{
			ID:        rec.ID,
			Name:      rec.Name,
			Schedule:  rec.Schedule,
			Src:       rec.Src,
			Ext:       strings.Split(rec.Ext, ","),
			Rules:     rules,
			CreatedAt: rec.CreatedAt,
			UpdatedAt: rec.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) CreateProgram(w http.ResponseWriter, r *http.Request) {
	var body api.ProgramInput
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	inp := normalizeInput(body)
	if inp.src == "" {
		writeError(w, http.StatusBadRequest, "src is required")
		return
	}
	for _, rule := range inp.rules {
		if err := workflow.ValidateRule(rule); err != nil {
			writeError(w, http.StatusBadRequest, "invalid rules: "+err.Error())
			return
		}
	}

	rulesJSON, err := marshalRules(inp.rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rules: "+err.Error())
		return
	}

	id := uuid.New().String()
	ext := strings.Join(inp.ext, ",")
	if err := s.db.CreateProgram(id, inp.name, inp.schedule, inp.src, ext, rulesJSON); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rec, _ := s.db.GetProgram(id)
	writeJSON(w, http.StatusCreated, api.Program{
		ID:        rec.ID,
		Name:      rec.Name,
		Schedule:  rec.Schedule,
		Src:       rec.Src,
		Ext:       strings.Split(rec.Ext, ","),
		Rules:     inp.rules,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	})
}

func (s *Server) GetProgram(w http.ResponseWriter, r *http.Request, id api.ProgramID) {
	rec, err := s.db.GetProgram(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "program not found")
		return
	}
	rules, _ := unmarshalRules(rec.RulesJSON)
	writeJSON(w, http.StatusOK, api.Program{
		ID:        rec.ID,
		Name:      rec.Name,
		Schedule:  rec.Schedule,
		Src:       rec.Src,
		Ext:       strings.Split(rec.Ext, ","),
		Rules:     rules,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	})
}

func (s *Server) UpdateProgram(w http.ResponseWriter, r *http.Request, id api.ProgramID) {
	rec, err := s.db.GetProgram(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "program not found")
		return
	}

	var body api.ProgramInput
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	inp := normalizeInput(body)
	if inp.src == "" {
		writeError(w, http.StatusBadRequest, "src is required")
		return
	}
	for _, rule := range inp.rules {
		if err := workflow.ValidateRule(rule); err != nil {
			writeError(w, http.StatusBadRequest, "invalid rules: "+err.Error())
			return
		}
	}

	rulesJSON, err := marshalRules(inp.rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rules: "+err.Error())
		return
	}

	if err := s.db.UpdateProgram(id, inp.name, inp.schedule, inp.src, strings.Join(inp.ext, ","), rulesJSON); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rec, _ = s.db.GetProgram(id)
	writeJSON(w, http.StatusOK, api.Program{
		ID:        rec.ID,
		Name:      rec.Name,
		Schedule:  rec.Schedule,
		Src:       rec.Src,
		Ext:       strings.Split(rec.Ext, ","),
		Rules:     inp.rules,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	})
}

func (s *Server) DeleteProgram(w http.ResponseWriter, r *http.Request, id api.ProgramID) {
	rec, err := s.db.GetProgram(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "program not found")
		return
	}
	if err := s.db.DeleteProgram(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) RunProgram(w http.ResponseWriter, r *http.Request, id api.ProgramID) {
	rec, err := s.db.GetProgram(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "program not found")
		return
	}

	rules, err := unmarshalRules(rec.RulesJSON)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "corrupt rules: "+err.Error())
		return
	}

	p := workflow.Program{
		Name:     rec.Name,
		Schedule: rec.Schedule,
		Src:      rec.Src,
		Ext:      strings.Split(rec.Ext, ","),
		Rules:    rules,
	}

	runID := uuid.New().String()
	if err := s.triggerRun(runID, p); err != nil {
		writeError(w, http.StatusInternalServerError, "start run: "+err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, api.RunAccepted{RunID: runID})
}

func (s *Server) ListRuns(w http.ResponseWriter, r *http.Request, params api.ListRunsParams) {
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	status := ""
	if params.Status != nil {
		status = string(*params.Status)
	}

	recs, err := s.db.RecentRuns(limit, status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]api.Run, 0, len(recs))
	for _, rec := range recs {
		out = append(out, api.Run{
			RunID:      rec.RunID,
			Name:       rec.Name,
			Status:     api.RunStatus(rec.Status),
			Total:      rec.Total,
			OK:         rec.OK,
			Failed:     rec.Failed,
			Skipped:    rec.Skipped,
			StartedAt:  rec.StartedAt,
			FinishedAt: rec.FinishedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) GetRun(w http.ResponseWriter, r *http.Request, id string) {
	rec, err := s.db.GetRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	jobs, err := s.db.LiveJobs(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jobsOut := make([]api.Job, 0, len(jobs))
	for _, j := range jobs {
		jobsOut = append(jobsOut, api.Job{
			ID:         j.ID,
			SourcePath: j.SourcePath,
			Status:     j.Status,
			Pct:        j.Pct,
			Speed:      j.Speed,
			UpdatedAt:  j.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, api.RunDetail{
		RunID:      rec.RunID,
		Name:       rec.Name,
		Status:     api.RunDetailStatus(rec.Status),
		Total:      rec.Total,
		OK:         rec.OK,
		Failed:     rec.Failed,
		Skipped:    rec.Skipped,
		StartedAt:  rec.StartedAt,
		FinishedAt: rec.FinishedAt,
		Jobs:       jobsOut,
	})
}

func (s *Server) CancelRun(w http.ResponseWriter, r *http.Request, id string) {
	rec, err := s.db.GetRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	killed := s.cancelRun(id)
	if !killed {
		// Run exists in DB but no live process — already finished or not started by this server.
		writeError(w, http.StatusConflict, "no live process found for run; it may have already finished")
		return
	}

	if err := s.db.CancelRun(id); err != nil {
		writeError(w, http.StatusInternalServerError, "cancel DB record: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
