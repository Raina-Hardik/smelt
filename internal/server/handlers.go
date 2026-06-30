package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// --- API types ---

type programInput struct {
	Name     string          `json:"name"`
	Schedule string          `json:"schedule"`
	Src      string          `json:"src"`
	Ext      []string        `json:"ext"`
	Rules    []workflow.Rule `json:"rules"`
}

type programResponse struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Schedule  string          `json:"schedule"`
	Src       string          `json:"src"`
	Ext       []string        `json:"ext"`
	Rules     []workflow.Rule `json:"rules"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type runResponse struct {
	RunID      string     `json:"run_id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Total      int        `json:"total"`
	OK         int        `json:"ok"`
	Failed     int        `json:"failed"`
	Skipped    int        `json:"skipped"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type jobResponse struct {
	ID         int64     `json:"id"`
	SourcePath string    `json:"source_path"`
	Status     string    `json:"status"`
	Pct        float64   `json:"pct"`
	Speed      string    `json:"speed,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type runDetailResponse struct {
	runResponse
	Jobs []jobResponse `json:"jobs"`
}

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

// --- Handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListPrograms(w http.ResponseWriter, _ *http.Request) {
	recs, err := s.db.ListPrograms()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]programResponse, 0, len(recs))
	for _, rec := range recs {
		rules, _ := unmarshalRules(rec.RulesJSON)
		out = append(out, programResponse{
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

func (s *Server) handleCreateProgram(w http.ResponseWriter, r *http.Request) {
	var inp programInput
	if err := decodeJSON(r, &inp); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if inp.Src == "" {
		writeError(w, http.StatusBadRequest, "src is required")
		return
	}
	if len(inp.Ext) == 0 {
		inp.Ext = []string{"mkv", "mp4", "avi"}
	}

	rulesJSON, err := marshalRules(inp.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rules: "+err.Error())
		return
	}

	id := uuid.New().String()
	ext := strings.Join(inp.Ext, ",")
	if err := s.db.CreateProgram(id, inp.Name, inp.Schedule, inp.Src, ext, rulesJSON); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rec, _ := s.db.GetProgram(id)
	writeJSON(w, http.StatusCreated, programResponse{
		ID:        rec.ID,
		Name:      rec.Name,
		Schedule:  rec.Schedule,
		Src:       rec.Src,
		Ext:       strings.Split(rec.Ext, ","),
		Rules:     inp.Rules,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	})
}

func (s *Server) handleGetProgram(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
	writeJSON(w, http.StatusOK, programResponse{
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

func (s *Server) handleUpdateProgram(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, err := s.db.GetProgram(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "program not found")
		return
	}

	var inp programInput
	if err := decodeJSON(r, &inp); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if inp.Src == "" {
		writeError(w, http.StatusBadRequest, "src is required")
		return
	}
	if len(inp.Ext) == 0 {
		inp.Ext = []string{"mkv", "mp4", "avi"}
	}

	rulesJSON, err := marshalRules(inp.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rules: "+err.Error())
		return
	}

	if err := s.db.UpdateProgram(id, inp.Name, inp.Schedule, inp.Src, strings.Join(inp.Ext, ","), rulesJSON); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rec, _ = s.db.GetProgram(id)
	writeJSON(w, http.StatusOK, programResponse{
		ID:        rec.ID,
		Name:      rec.Name,
		Schedule:  rec.Schedule,
		Src:       rec.Src,
		Ext:       strings.Split(rec.Ext, ","),
		Rules:     inp.Rules,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	})
}

func (s *Server) handleDeleteProgram(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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

func (s *Server) handleRunProgram(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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

	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}
	status := r.URL.Query().Get("status")

	recs, err := s.db.RecentRuns(limit, status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]runResponse, 0, len(recs))
	for _, rec := range recs {
		out = append(out, runResponse{
			RunID:      rec.RunID,
			Name:       rec.Name,
			Status:     rec.Status,
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

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
	jobsOut := make([]jobResponse, 0, len(jobs))
	for _, j := range jobs {
		jobsOut = append(jobsOut, jobResponse{
			ID:         j.ID,
			SourcePath: j.SourcePath,
			Status:     j.Status,
			Pct:        j.Pct,
			Speed:      j.Speed,
			UpdatedAt:  j.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, runDetailResponse{
		runResponse: runResponse{
			RunID:      rec.RunID,
			Name:       rec.Name,
			Status:     rec.Status,
			Total:      rec.Total,
			OK:         rec.OK,
			Failed:     rec.Failed,
			Skipped:    rec.Skipped,
			StartedAt:  rec.StartedAt,
			FinishedAt: rec.FinishedAt,
		},
		Jobs: jobsOut,
	})
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
